package query

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/databricks/cli/cmd/root"
	"github.com/databricks/cli/libs/cmdctx"
	"github.com/databricks/cli/libs/cmdio"
	"github.com/databricks/cli/libs/databrickscfg/profile"
	"github.com/databricks/cli/libs/env"
	"github.com/databricks/cli/libs/safety/sqlsafe"
	databricks "github.com/databricks/databricks-sdk-go"
	sqlapi "github.com/databricks/databricks-sdk-go/service/sql"
)

type sqlFormat string

const (
	formatTable sqlFormat = "table"
	formatJSON  sqlFormat = "json"
	formatCSV   sqlFormat = "csv"
)

type sqlOptions struct {
	warehouseID          string
	inlineSQL            string
	file                 string
	rawFormat            string
	waitTimeout          time.Duration
	resultFile           string
	allowDestructiveFlag bool
}

func newSQLCommand() *cobra.Command {
	opts := &sqlOptions{}

	cmd := &cobra.Command{
		Use:   "sql",
		Short: "Execute SQL using the Databricks Statement Execution API",
		PreRunE: func(cmd *cobra.Command, args []string) error {
			return root.MustWorkspaceClient(cmd, args)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runSQLCommand(cmd, opts)
		},
	}

	cmd.Flags().StringVar(&opts.warehouseID, "warehouse-id", "", "ID of the SQL warehouse to use")
	cmd.Flags().StringVar(&opts.inlineSQL, "sql", "", "Inline SQL statement to execute")
	cmd.Flags().StringVar(&opts.file, "file", "", "Path to a file containing SQL statements")
	cmd.Flags().StringVar(&opts.rawFormat, "format", "", "Output format: table, json, or csv")
	cmd.Flags().DurationVar(&opts.waitTimeout, "wait-timeout", 10*time.Second, "Maximum time to wait synchronously before polling for results")
	cmd.Flags().StringVar(&opts.resultFile, "result-file", "", "Write query results to the specified file path")
	cmd.Flags().BoolVar(&opts.allowDestructiveFlag, "allow-destructive", false, "Allow statements that may have side effects")

	cmd.MarkFlagRequired("warehouse-id")

	return cmd
}

func runSQLCommand(cmd *cobra.Command, opts *sqlOptions) error {
	if opts.inlineSQL == "" && opts.file == "" {
		return fmt.Errorf("either --sql or --file must be specified")
	}
	if opts.inlineSQL != "" && opts.file != "" {
		return fmt.Errorf("--sql and --file cannot be used together")
	}

	sqlText, err := loadSQLText(opts)
	if err != nil {
		return err
	}

	statements, err := sqlsafe.ParseStatements(sqlText)
	if err != nil {
		return err
	}
	if len(statements) == 0 {
		return fmt.Errorf("no SQL statements to execute")
	}

	allowDestructive, err := resolveAllowDestructive(cmd, opts.allowDestructiveFlag)
	if err != nil {
		return err
	}

	if !allowDestructive {
		classifier := sqlsafe.NewClassifier(sqlsafe.DefaultPolicy())
		if err := classifier.Check(statements); err != nil {
			return fmt.Errorf("blocked by read-only policy: %w. Pass --allow-destructive or set DATABRICKS_CLI_ALLOW_DESTRUCTIVE_SQL=true to override", err)
		}
	}

	format, err := determineFormat(cmd, opts.rawFormat, opts.resultFile)
	if err != nil {
		return err
	}

	waitTimeout, err := normalizeWaitTimeout(opts.waitTimeout)
	if err != nil {
		return err
	}

	var output io.Writer = cmd.OutOrStdout()
	var closer io.Closer
	if opts.resultFile != "" {
		file, err := createResultFile(opts.resultFile)
		if err != nil {
			return err
		}
		closer = file
		output = file
	}
	if closer != nil {
		defer closer.Close()
	}

	client := cmdctx.WorkspaceClient(cmd.Context())
	return executeStatements(cmd.Context(), client, statements, opts.warehouseID, waitTimeout, format, output, cmd)
}

func loadSQLText(opts *sqlOptions) (string, error) {
	if opts.inlineSQL != "" {
		return opts.inlineSQL, nil
	}
	data, err := os.ReadFile(opts.file)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func determineFormat(cmd *cobra.Command, raw string, resultFile string) (sqlFormat, error) {
	if raw != "" {
		f := sqlFormat(strings.ToLower(raw))
		if !f.valid() {
			return "", fmt.Errorf("unknown format %q", raw)
		}
		return f, nil
	}
	if resultFile != "" {
		return formatJSON, nil
	}
	if cmdio.IsInteractive(cmd.Context()) {
		return formatTable, nil
	}
	return formatJSON, nil
}

func (f sqlFormat) valid() bool {
	switch f {
	case formatTable, formatJSON, formatCSV:
		return true
	default:
		return false
	}
}

func normalizeWaitTimeout(d time.Duration) (string, error) {
	if d < 0 {
		return "", fmt.Errorf("wait-timeout must be non-negative")
	}
	if d == 0 {
		return "0s", nil
	}
	if d%time.Second != 0 {
		return "", fmt.Errorf("wait-timeout must be specified in whole seconds")
	}
	seconds := int(d / time.Second)
	if seconds < 5 || seconds > 50 {
		return "", fmt.Errorf("wait-timeout must be between 5s and 50s, or 0s for async")
	}
	return fmt.Sprintf("%ds", seconds), nil
}

func createResultFile(path string) (*os.File, error) {
	dir := filepath.Dir(path)
	if dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	return os.Create(path)
}

func resolveAllowDestructive(cmd *cobra.Command, flagValue bool) (bool, error) {
	if cmd.Flags().Changed("allow-destructive") {
		return flagValue, nil
	}

	ctx := cmd.Context()
	if raw, ok := env.Lookup(ctx, "DATABRICKS_CLI_ALLOW_DESTRUCTIVE_SQL"); ok && raw != "" {
		val, err := strconv.ParseBool(raw)
		if err != nil {
			return false, fmt.Errorf("invalid value for DATABRICKS_CLI_ALLOW_DESTRUCTIVE_SQL: %s", raw)
		}
		return val, nil
	}

	cfg := cmdctx.ConfigUsed(ctx)
	if cfg != nil && cfg.Profile != "" {
		profiler := profile.FileProfilerImpl{}
		file, err := profiler.Get(ctx)
		if err != nil {
			if errors.Is(err, profile.ErrNoConfiguration) {
				return false, nil
			}
			return false, err
		}
		section := file.Section(cfg.Profile)
		if section != nil && section.HasKey("cli.allow_destructive_sql") {
			raw := section.Key("cli.allow_destructive_sql").String()
			if raw != "" {
				val, err := strconv.ParseBool(raw)
				if err != nil {
					return false, fmt.Errorf("invalid boolean for cli.allow_destructive_sql in profile %s: %s", cfg.Profile, raw)
				}
				return val, nil
			}
		}
	}
	return false, nil
}

type statementRenderer interface {
	Begin(resp *sqlapi.StatementResponse) error
	AddChunk(chunk *sqlapi.ResultData) error
	End() error
}

func executeStatements(ctx context.Context, client *databricks.WorkspaceClient, statements []sqlsafe.Statement, warehouseID string, waitTimeout string, format sqlFormat, out io.Writer, cmd *cobra.Command) error {
	var collector *jsonCollector
	if format == formatJSON {
		collector = &jsonCollector{}
	}

	for i, stmt := range statements {
		sqlText := stmt.Text
		spinner := cmdio.Spinner(ctx)
		spinner <- fmt.Sprintf("Submitting statement %d", i+1)

		resp, err := client.StatementExecution.ExecuteStatement(ctx, sqlapi.ExecuteStatementRequest{
			WarehouseId:   warehouseID,
			Statement:     sqlText,
			WaitTimeout:   waitTimeout,
			OnWaitTimeout: sqlapi.ExecuteStatementRequestOnWaitTimeoutContinue,
		})
		if err != nil {
			close(spinner)
			return err
		}

		finalResp, err := waitForCompletion(ctx, client, resp, spinner)
		close(spinner)
		if err != nil {
			return err
		}
		if err := ensureSucceeded(finalResp); err != nil {
			return err
		}

		if i > 0 && (format == formatTable || format == formatCSV) {
			fmt.Fprintln(out)
		}

		switch format {
		case formatTable:
			renderer := newTableRenderer(out)
			if err := renderStatement(ctx, client, finalResp, renderer); err != nil {
				return err
			}
		case formatCSV:
			renderer := newCSVRenderer(out)
			if err := renderStatement(ctx, client, finalResp, renderer); err != nil {
				return err
			}
		case formatJSON:
			renderer := collector.begin(finalResp)
			if err := renderStatement(ctx, client, finalResp, renderer); err != nil {
				return err
			}
		}
	}

	if format == formatJSON {
		return collector.write(out)
	}
	return nil
}

func waitForCompletion(ctx context.Context, client *databricks.WorkspaceClient, resp *sqlapi.StatementResponse, spinner chan string) (*sqlapi.StatementResponse, error) {
	current := resp
	state := getState(current)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for state == sqlapi.StatementStatePending || state == sqlapi.StatementStateRunning {
		if spinner != nil {
			spinner <- fmt.Sprintf("Statement %s is %s", current.StatementId, strings.ToLower(state.String()))
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
		}
		next, err := client.StatementExecution.GetStatementByStatementId(ctx, current.StatementId)
		if err != nil {
			return nil, err
		}
		current = next
		state = getState(current)
	}
	return current, nil
}

func ensureSucceeded(resp *sqlapi.StatementResponse) error {
	if resp == nil || resp.Status == nil {
		return nil
	}
	switch resp.Status.State {
	case sqlapi.StatementStateSucceeded, sqlapi.StatementStateClosed:
		return nil
	case sqlapi.StatementStateCanceled:
		return fmt.Errorf("statement %s was canceled", resp.StatementId)
	case sqlapi.StatementStateFailed:
		if resp.Status.Error != nil {
			return fmt.Errorf("statement %s failed: %s", resp.StatementId, resp.Status.Error.Message)
		}
		return fmt.Errorf("statement %s failed", resp.StatementId)
	default:
		return nil
	}
}

func getState(resp *sqlapi.StatementResponse) sqlapi.StatementState {
	if resp == nil || resp.Status == nil {
		return ""
	}
	return resp.Status.State
}

func renderStatement(ctx context.Context, client *databricks.WorkspaceClient, resp *sqlapi.StatementResponse, renderer statementRenderer) error {
	if err := renderer.Begin(resp); err != nil {
		return err
	}
	indices := collectChunkIndices(resp)
	if len(indices) == 0 {
		if resp.Result != nil {
			if err := renderer.AddChunk(resp.Result); err != nil {
				return err
			}
		}
		return renderer.End()
	}
	seen := false
	for _, idx := range indices {
		chunk, err := getChunk(ctx, client, resp, idx)
		if err != nil {
			return err
		}
		if chunk == nil {
			continue
		}
		seen = true
		if err := renderer.AddChunk(chunk); err != nil {
			return err
		}
	}
	if !seen && resp.Result != nil {
		if err := renderer.AddChunk(resp.Result); err != nil {
			return err
		}
	}
	return renderer.End()
}

func collectChunkIndices(resp *sqlapi.StatementResponse) []int {
	if resp.Manifest != nil && len(resp.Manifest.Chunks) > 0 {
		indices := make([]int, 0, len(resp.Manifest.Chunks))
		for _, chunk := range resp.Manifest.Chunks {
			indices = append(indices, chunk.ChunkIndex)
		}
		sort.Ints(indices)
		return indices
	}
	if resp.Result != nil {
		return []int{resp.Result.ChunkIndex}
	}
	return nil
}

func getChunk(ctx context.Context, client *databricks.WorkspaceClient, resp *sqlapi.StatementResponse, idx int) (*sqlapi.ResultData, error) {
	if resp.Result != nil && resp.Result.ChunkIndex == idx {
		return resp.Result, nil
	}
	return client.StatementExecution.GetStatementResultChunkN(ctx, sqlapi.GetStatementResultChunkNRequest{
		StatementId: resp.StatementId,
		ChunkIndex:  idx,
	})
}

type tableRenderer struct {
	out       io.Writer
	writer    *tabwriter.Writer
	manifest  *sqlapi.ResultManifest
	columns   []string
	statement string
	headerSet bool
}

func newTableRenderer(out io.Writer) *tableRenderer {
	return &tableRenderer{out: out}
}

func (r *tableRenderer) Begin(resp *sqlapi.StatementResponse) error {
	r.manifest = resp.Manifest
	r.statement = resp.StatementId
	if r.manifest != nil && r.manifest.Schema != nil && len(r.manifest.Schema.Columns) > 0 {
		r.columns = columnNamesFromManifest(r.manifest)
		r.initWriter()
		r.writeHeader()
	}
	return nil
}

func (r *tableRenderer) AddChunk(chunk *sqlapi.ResultData) error {
	if r.writer == nil {
		if len(r.columns) == 0 {
			r.columns = inferColumns(r.manifest, chunk)
		}
		if len(r.columns) > 0 {
			r.initWriter()
			r.writeHeader()
		}
	}
	if r.writer == nil {
		return nil
	}
	for _, row := range chunk.DataArray {
		fmt.Fprintln(r.writer, strings.Join(padRow(row, len(r.columns)), "\t"))
	}
	return nil
}

func (r *tableRenderer) End() error {
	if r.writer != nil {
		return r.writer.Flush()
	}
	fmt.Fprintf(r.out, "Statement %s returned no results\n", r.statement)
	return nil
}

func (r *tableRenderer) initWriter() {
	if r.writer == nil {
		r.writer = tabwriter.NewWriter(r.out, 0, 8, 2, ' ', 0)
	}
}

func (r *tableRenderer) writeHeader() {
	if r.writer == nil || r.headerSet {
		return
	}
	fmt.Fprintln(r.writer, strings.Join(r.columns, "\t"))
	r.headerSet = true
}

type csvRenderer struct {
	writer   *csv.Writer
	manifest *sqlapi.ResultManifest
	columns  []string
	header   bool
}

func newCSVRenderer(out io.Writer) *csvRenderer {
	return &csvRenderer{writer: csv.NewWriter(out)}
}

func (r *csvRenderer) Begin(resp *sqlapi.StatementResponse) error {
	r.manifest = resp.Manifest
	return nil
}

func (r *csvRenderer) AddChunk(chunk *sqlapi.ResultData) error {
	if len(r.columns) == 0 {
		r.columns = inferColumns(r.manifest, chunk)
		if len(r.columns) > 0 && !r.header {
			if err := r.writer.Write(r.columns); err != nil {
				return err
			}
			r.header = true
		}
	}
	if len(r.columns) == 0 {
		return nil
	}
	for _, row := range chunk.DataArray {
		if err := r.writer.Write(padRow(row, len(r.columns))); err != nil {
			return err
		}
	}
	return nil
}

func (r *csvRenderer) End() error {
	r.writer.Flush()
	return r.writer.Error()
}

type jsonStatementRenderer struct {
	collector *jsonCollector
	result    *jsonStatementResult
	columns   []string
}

func (j *jsonStatementRenderer) Begin(resp *sqlapi.StatementResponse) error {
	j.result = j.collector.addStatement(resp)
	if resp.Manifest != nil && resp.Manifest.Schema != nil && len(resp.Manifest.Schema.Columns) > 0 {
		j.columns = columnNamesFromManifest(resp.Manifest)
	}
	return nil
}

func (j *jsonStatementRenderer) AddChunk(chunk *sqlapi.ResultData) error {
	if j.result == nil {
		return nil
	}
	if j.columns == nil {
		j.columns = inferColumns(j.result.Manifest, chunk)
	}
	for _, row := range chunk.DataArray {
		record := map[string]string{}
		if len(j.columns) == 0 {
			for idx, value := range row {
				record[fmt.Sprintf("col_%d", idx+1)] = value
			}
		} else {
			padded := padRow(row, len(j.columns))
			for idx, name := range j.columns {
				record[name] = padded[idx]
			}
		}
		j.result.Rows = append(j.result.Rows, record)
	}
	return nil
}

func (j *jsonStatementRenderer) End() error {
	j.result = nil
	j.columns = nil
	return nil
}

type jsonCollector struct {
	results []jsonStatementResult
}

type jsonStatementResult struct {
	StatementID string                 `json:"statement_id"`
	Manifest    *sqlapi.ResultManifest `json:"manifest,omitempty"`
	Rows        []map[string]string    `json:"rows"`
}

func (c *jsonCollector) begin(resp *sqlapi.StatementResponse) *jsonStatementRenderer {
	return &jsonStatementRenderer{collector: c}
}

func (c *jsonCollector) addStatement(resp *sqlapi.StatementResponse) *jsonStatementResult {
	result := jsonStatementResult{
		StatementID: resp.StatementId,
		Manifest:    resp.Manifest,
		Rows:        make([]map[string]string, 0),
	}
	c.results = append(c.results, result)
	return &c.results[len(c.results)-1]
}

func (c *jsonCollector) write(out io.Writer) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(c.results)
}

func columnNamesFromManifest(manifest *sqlapi.ResultManifest) []string {
	if manifest == nil || manifest.Schema == nil {
		return nil
	}
	cols := make([]string, len(manifest.Schema.Columns))
	for i, col := range manifest.Schema.Columns {
		name := col.Name
		if name == "" {
			name = fmt.Sprintf("col_%d", i+1)
		}
		cols[i] = name
	}
	return cols
}

func inferColumns(manifest *sqlapi.ResultManifest, chunk *sqlapi.ResultData) []string {
	if names := columnNamesFromManifest(manifest); len(names) > 0 {
		return names
	}
	if chunk != nil && len(chunk.DataArray) > 0 {
		rowLen := len(chunk.DataArray[0])
		cols := make([]string, rowLen)
		for i := range cols {
			cols[i] = fmt.Sprintf("col_%d", i+1)
		}
		return cols
	}
	return nil
}

func padRow(row []string, cols int) []string {
	out := make([]string, cols)
	for i := range out {
		if i < len(row) {
			out[i] = row[i]
			continue
		}
		out[i] = ""
	}
	return out
}
