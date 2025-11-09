package query

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/databricks/databricks-sdk-go/config"
	dbsql "github.com/databricks/databricks-sdk-go/service/sql"

	"github.com/databricks/cli/cmd/root"
	"github.com/databricks/cli/libs/cmdctx"
	"github.com/databricks/cli/libs/cmdio"
	"github.com/databricks/cli/libs/env"
	"github.com/databricks/cli/libs/safety/sqlsafe"
)

type sqlOptions struct {
	warehouseID      string
	inlineSQL        string
	sqlFile          string
	format           string
	resultFile       string
	waitTimeout      time.Duration
	allowDestructive bool
}

func newSQLCommand() *cobra.Command {
	opts := &sqlOptions{
		waitTimeout: 10 * time.Second,
	}

	cmd := &cobra.Command{
		Use:   "sql",
		Short: "Execute SQL statements on a Databricks SQL warehouse",
		Long: `Run SQL statements via the Statement Execution API.

By default the command enforces a read-only safety gate and blocks statements
that may mutate data or metadata.`,
		Args:         cobra.NoArgs,
		PreRunE:      root.MustWorkspaceClient,
		SilenceUsage: true,
		RunE:         opts.run,
	}

	flags := cmd.Flags()
	flags.StringVar(&opts.warehouseID, "warehouse-id", "", "SQL warehouse ID to execute the statement against")
	flags.StringVar(&opts.inlineSQL, "sql", "", "Inline SQL statement to execute")
	flags.StringVar(&opts.sqlFile, "file", "", "Path to a file containing SQL statements")
	flags.StringVar(&opts.format, "format", "", "Output format: table, json, or csv")
	flags.StringVar(&opts.resultFile, "result-file", "", "Path to write JSON or CSV results")
	flags.DurationVar(&opts.waitTimeout, "wait-timeout", 10*time.Second, "Time to wait synchronously before polling for results")
	flags.BoolVar(&opts.allowDestructive, "allow-destructive", false, "Allow statements that may modify data or metadata")

	return cmd
}

func (o *sqlOptions) run(cmd *cobra.Command, _ []string) error {
	ctx := cmd.Context()

	if o.warehouseID == "" {
		return fmt.Errorf("required flag \"--warehouse-id\" not set")
	}

	if (o.inlineSQL == "" && o.sqlFile == "") || (o.inlineSQL != "" && o.sqlFile != "") {
		return fmt.Errorf("specify exactly one of --sql or --file")
	}

	var script string
	if o.inlineSQL != "" {
		script = o.inlineSQL
	} else {
		data, err := os.ReadFile(o.sqlFile)
		if err != nil {
			return fmt.Errorf("failed to read SQL file: %w", err)
		}
		script = string(data)
	}

	statements := sqlsafe.ParseStatements(script)
	if len(statements) == 0 {
		return fmt.Errorf("no SQL statements to execute")
	}

	if o.waitTimeout < 0 {
		return fmt.Errorf("wait-timeout must be non-negative")
	}

	allowDestructive, err := o.resolveAllowDestructive(cmd)
	if err != nil {
		return err
	}

	if !allowDestructive {
		classifier := sqlsafe.NewClassifier(nil)
		results, err := classifier.Classify(statements)
		if err != nil {
			return err
		}
		for idx, res := range results {
			if !res.Allowed {
				return fmt.Errorf(
					"blocked by safe-only mode: %s at statement %d (pos %d:%d). Pass --allow-destructive or set cli.allow_destructive_sql=true to override.",
					res.Reason,
					idx+1,
					res.Position.Line,
					res.Position.Column,
				)
			}
		}
	}

	format := strings.ToLower(o.format)
	if format == "" {
		if o.resultFile != "" {
			format = "json"
		} else if cmdio.IsInteractive(ctx) {
			format = "table"
		} else {
			format = "json"
		}
	}

	switch format {
	case "table", "json", "csv":
	default:
		return fmt.Errorf("unknown format %q; must be table, json, or csv", format)
	}

	if o.resultFile != "" && format == "table" {
		return fmt.Errorf("table format cannot be written to --result-file; use json or csv")
	}

	execution := cmdctx.WorkspaceClient(ctx).StatementExecution

	results := make([]*statementExecutionResult, 0, len(statements))
	for _, stmt := range statements {
		text := strings.TrimSpace(stmt.Text)
		if text == "" {
			continue
		}
		result, err := o.executeStatement(ctx, execution, text)
		if err != nil {
			return err
		}
		results = append(results, result)
	}

	if len(results) == 0 {
		return fmt.Errorf("no SQL statements to execute after filtering")
	}

	switch format {
	case "table":
		return o.renderTable(cmd, results)
	case "json":
		return o.renderJSON(cmd, results)
	case "csv":
		return o.renderCSV(cmd, results)
	default:
		return nil
	}
}

func (o *sqlOptions) resolveAllowDestructive(cmd *cobra.Command) (bool, error) {
	ctx := cmd.Context()
	if cmd.Flags().Changed("allow-destructive") {
		return o.allowDestructive, nil
	}

	if value, ok := env.Lookup(ctx, "DATABRICKS_CLI_ALLOW_DESTRUCTIVE_SQL"); ok {
		value = strings.TrimSpace(value)
		if value == "" {
			return false, nil
		}
		allowed, err := strconv.ParseBool(value)
		if err != nil {
			return false, fmt.Errorf("invalid value for DATABRICKS_CLI_ALLOW_DESTRUCTIVE_SQL: %w", err)
		}
		return allowed, nil
	}

	cfg := cmdctx.ConfigUsed(ctx)
	if cfg == nil {
		return false, nil
	}

	profileName := cfg.Profile
	if profileName == "" {
		profileName = "DEFAULT"
	}

	configFile, err := config.LoadFile(cfg.ConfigFile)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return false, nil
		}
		return false, fmt.Errorf("failed to load config file: %w", err)
	}

	section, err := configFile.GetSection(profileName)
	if err != nil {
		return false, nil
	}

	key, err := section.GetKey("cli.allow_destructive_sql")
	if err != nil {
		return false, nil
	}
	value := strings.TrimSpace(key.Value())
	if value == "" {
		return false, nil
	}
	allowed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("profile %s: invalid cli.allow_destructive_sql value: %w", profileName, err)
	}
	return allowed, nil
}

func (o *sqlOptions) executeStatement(ctx context.Context, execution dbsql.StatementExecutionInterface, statement string) (*statementExecutionResult, error) {
	req := dbsql.ExecuteStatementRequest{
		Statement:     statement,
		WarehouseId:   o.warehouseID,
		WaitTimeout:   o.waitTimeout.String(),
		OnWaitTimeout: dbsql.ExecuteStatementRequestOnWaitTimeoutContinue,
	}

	resp, err := execution.ExecuteStatement(ctx, req)
	if err != nil {
		return nil, err
	}

	final, err := o.waitForStatement(ctx, execution, resp)
	if err != nil {
		return nil, err
	}

	rows, err := collectRows(ctx, execution, final)
	if err != nil {
		return nil, err
	}

	return &statementExecutionResult{
		StatementID: final.StatementId,
		Manifest:    final.Manifest,
		Rows:        rows,
	}, nil
}

func (o *sqlOptions) waitForStatement(ctx context.Context, execution dbsql.StatementExecutionInterface, resp *dbsql.StatementResponse) (*dbsql.StatementResponse, error) {
	if resp.Status == nil {
		return nil, fmt.Errorf("statement response missing status")
	}

	state := resp.Status.State
	if state == dbsql.StatementStateSucceeded {
		return resp, nil
	}

	if isTerminalState(state) {
		return nil, buildStatementError(resp)
	}

	spinner := cmdio.Spinner(ctx)
	if spinner != nil {
		spinner <- fmt.Sprintf("statement %s running", resp.StatementId)
	}
	defer func() {
		if spinner != nil {
			close(spinner)
		}
	}()

	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			next, err := execution.GetStatementByStatementId(ctx, resp.StatementId)
			if err != nil {
				return nil, err
			}
			if next.Status == nil {
				return nil, fmt.Errorf("statement status missing in polling response")
			}
			if spinner != nil {
				spinner <- fmt.Sprintf("statement %s %s", resp.StatementId, strings.ToLower(next.Status.State.String()))
			}
			state = next.Status.State
			switch {
			case state == dbsql.StatementStateSucceeded:
				return next, nil
			case isTerminalState(state):
				return nil, buildStatementError(next)
			}
		}
	}
}

func isTerminalState(state dbsql.StatementState) bool {
	switch state {
	case dbsql.StatementStateFailed, dbsql.StatementStateCanceled, dbsql.StatementStateClosed:
		return true
	default:
		return false
	}
}

func buildStatementError(resp *dbsql.StatementResponse) error {
	if resp == nil || resp.Status == nil {
		return fmt.Errorf("statement failed")
	}
	status := resp.Status
	if status.Error != nil {
		return fmt.Errorf("statement %s %s: %s %s", resp.StatementId, status.State.String(), status.Error.ErrorCode, status.Error.Message)
	}
	return fmt.Errorf("statement %s %s", resp.StatementId, status.State.String())
}

func collectRows(ctx context.Context, execution dbsql.StatementExecutionInterface, resp *dbsql.StatementResponse) ([][]string, error) {
	if resp.Result == nil {
		return nil, nil
	}
	it := newResultIterator(ctx, execution, resp.StatementId, resp.Result)
	var rows [][]string
	for {
		row, err := it.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
		rows = append(rows, row)
	}
	return rows, nil
}

type statementExecutionResult struct {
	StatementID string
	Manifest    *dbsql.ResultManifest
	Rows        [][]string
}

type resultIterator struct {
	ctx         context.Context
	execution   dbsql.StatementExecutionInterface
	statementID string
	current     *dbsql.ResultData
	index       int
}

func newResultIterator(ctx context.Context, execution dbsql.StatementExecutionInterface, statementID string, initial *dbsql.ResultData) *resultIterator {
	return &resultIterator{
		ctx:         ctx,
		execution:   execution,
		statementID: statementID,
		current:     initial,
		index:       0,
	}
}

func (r *resultIterator) Next() ([]string, error) {
	for {
		if r.current == nil {
			return nil, io.EOF
		}
		if r.index < len(r.current.DataArray) {
			row := r.current.DataArray[r.index]
			r.index++
			copied := make([]string, len(row))
			copy(copied, row)
			return copied, nil
		}
		nextIndex := r.current.NextChunkIndex
		if nextIndex == 0 {
			r.current = nil
			continue
		}
		data, err := r.execution.GetStatementResultChunkNByStatementIdAndChunkIndex(r.ctx, r.statementID, nextIndex)
		if err != nil {
			return nil, err
		}
		r.current = data
		r.index = 0
	}
}

func (o *sqlOptions) renderTable(cmd *cobra.Command, results []*statementExecutionResult) error {
	out := cmd.OutOrStdout()
	for idx, res := range results {
		if idx > 0 {
			fmt.Fprintln(out)
		}
		if len(results) > 1 {
			fmt.Fprintf(out, "Statement %d (id: %s)\n", idx+1, res.StatementID)
		}
		schema := []dbsql.ColumnInfo{}
		if res.Manifest != nil && res.Manifest.Schema != nil {
			schema = res.Manifest.Schema.Columns
		}
		if len(schema) == 0 && len(res.Rows) == 0 {
			fmt.Fprintln(out, "(no results)")
		} else {
			tw := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
			if len(schema) > 0 {
				for i, col := range schema {
					if i > 0 {
						fmt.Fprint(tw, "\t")
					}
					fmt.Fprint(tw, col.Name)
				}
				fmt.Fprint(tw, "\n")
			}
			for _, row := range res.Rows {
				for i, cell := range row {
					if i > 0 {
						fmt.Fprint(tw, "\t")
					}
					fmt.Fprint(tw, cell)
				}
				fmt.Fprint(tw, "\n")
			}
			if err := tw.Flush(); err != nil {
				return err
			}
		}
		if res.Manifest != nil && res.Manifest.Truncated {
			fmt.Fprintf(cmd.ErrOrStderr(), "Results truncated for statement %s\n", res.StatementID)
		}
	}
	return nil
}

func (o *sqlOptions) renderJSON(cmd *cobra.Command, results []*statementExecutionResult) error {
	var writer io.WriteCloser
	if o.resultFile != "" {
		if err := os.MkdirAll(filepath.Dir(o.resultFile), 0o755); err != nil {
			return fmt.Errorf("failed to create result directory: %w", err)
		}
		f, err := os.Create(o.resultFile)
		if err != nil {
			return fmt.Errorf("failed to create result file: %w", err)
		}
		writer = f
	} else {
		writer = nopWriteCloser{Writer: cmd.OutOrStdout()}
	}
	defer writer.Close()

	payload := make([]jsonStatement, len(results))
	for i, res := range results {
		js := jsonStatement{
			StatementID: res.StatementID,
			Manifest:    res.Manifest,
			Rows:        res.Rows,
		}
		if res.Manifest != nil && res.Manifest.Schema != nil {
			js.Schema = res.Manifest.Schema.Columns
		}
		payload[i] = js
		if res.Manifest != nil && res.Manifest.Truncated {
			fmt.Fprintf(cmd.ErrOrStderr(), "Results truncated for statement %s\n", res.StatementID)
		}
	}

	enc := json.NewEncoder(writer)
	enc.SetIndent("", "  ")
	if len(payload) == 1 {
		return enc.Encode(payload[0])
	}
	return enc.Encode(payload)
}

func (o *sqlOptions) renderCSV(cmd *cobra.Command, results []*statementExecutionResult) error {
	if len(results) != 1 {
		return fmt.Errorf("csv output requires exactly one statement")
	}
	res := results[0]

	var writer io.WriteCloser
	if o.resultFile != "" {
		if err := os.MkdirAll(filepath.Dir(o.resultFile), 0o755); err != nil {
			return fmt.Errorf("failed to create result directory: %w", err)
		}
		f, err := os.Create(o.resultFile)
		if err != nil {
			return fmt.Errorf("failed to create result file: %w", err)
		}
		writer = f
	} else {
		writer = nopWriteCloser{Writer: cmd.OutOrStdout()}
	}
	defer writer.Close()

	csvWriter := csv.NewWriter(writer)
	if res.Manifest != nil && res.Manifest.Schema != nil {
		headers := make([]string, len(res.Manifest.Schema.Columns))
		for i, col := range res.Manifest.Schema.Columns {
			headers[i] = col.Name
		}
		if len(headers) > 0 {
			if err := csvWriter.Write(headers); err != nil {
				return err
			}
		}
	}
	for _, row := range res.Rows {
		if err := csvWriter.Write(row); err != nil {
			return err
		}
	}
	csvWriter.Flush()
	if err := csvWriter.Error(); err != nil {
		return err
	}
	if res.Manifest != nil && res.Manifest.Truncated {
		fmt.Fprintf(cmd.ErrOrStderr(), "Results truncated for statement %s\n", res.StatementID)
	}
	return nil
}

type jsonStatement struct {
	StatementID string                `json:"statement_id"`
	Manifest    *dbsql.ResultManifest `json:"manifest,omitempty"`
	Schema      []dbsql.ColumnInfo    `json:"schema,omitempty"`
	Rows        [][]string            `json:"rows"`
}

type nopWriteCloser struct {
	io.Writer
}

func (n nopWriteCloser) Close() error { return nil }
