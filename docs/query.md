# `databricks query`

The `databricks query` command group runs ad hoc queries against a Databricks SQL
warehouse using the [Statement Execution API](https://docs.databricks.com/en/sql/api/statement-execution/index.html).
The initial `sql` subcommand executes SQL statements and returns the results as
tables, JSON, or CSV.

## Examples

Run an inline statement:

```bash
databricks query sql --warehouse-id <WAREHOUSE_ID> --sql "select 1"
```

Run statements from a file:

```bash
databricks query sql --warehouse-id <WAREHOUSE_ID> --file queries/top10.sql
```

Write results as JSON:

```bash
databricks query sql --warehouse-id <WAREHOUSE_ID> --sql "select * from system.builtin.current_queries" \
  --format json --result-file out/current_queries.json
```

By default the command waits up to 10 seconds for the statement to finish. If it
is still running, the CLI transparently polls the statement until it succeeds or
fails. Set `--wait-timeout 0s` to return immediately and use the printed
statement ID to poll later.

## Safety gate

The CLI blocks statements that can modify data or metadata unless explicitly
enabled. The following operations are allowed by default:

- `SELECT`
- `SHOW`
- `DESCRIBE`
- `WITH` clauses that resolve to one of the above keywords
- `EXPLAIN` statements whose target statement is read-only

All other statements return an error similar to:

```
Error: blocked by safe-only mode: found DDL/DML keyword CREATE at statement 1 (pos 1:1).
```

Override this behaviour using one of the following options:

- `--allow-destructive` flag on the command
- `DATABRICKS_CLI_ALLOW_DESTRUCTIVE_SQL=true` environment variable
- `cli.allow_destructive_sql = true` profile setting in `~/.databrickscfg`

Flags take precedence over the environment variable, which in turn takes
precedence over the profile setting.

## Output formats

The CLI automatically chooses `table` output for interactive terminals and `json`
otherwise. Override the format with `--format table|json|csv`. When writing JSON
or CSV to disk, use `--result-file <path>` to write the data to a file instead of
stdout.
