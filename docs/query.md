# `databricks query`

The `databricks query` command submits SQL statements to a Databricks SQL Warehouse
through the [Statement Execution API](https://docs.databricks.com/api/workspace/sql-statements). Use it for
non-interactive workflows such as scripts, CI jobs, or tooling that requires
structured responses.

## Running queries

Execute an inline statement:

```bash
databricks query sql \
  --warehouse-id <WAREHOUSE_ID> \
  --sql "SELECT 1 AS one"
```

Read the statement from a file (multiple statements separated by semicolons are
executed sequentially):

```bash
databricks query sql \
  --warehouse-id <WAREHOUSE_ID> \
  --file queries/example.sql
```

## Output formats

The CLI renders results as a table when writing to an interactive terminal. You
can choose a specific format with `--format` and optionally redirect the output
to a file with `--result-file`.

```bash
# JSON to a file
databricks query sql \
  --warehouse-id <WAREHOUSE_ID> \
  --sql "SELECT * FROM system.builtin.current_queries" \
  --format json \
  --result-file current_queries.json

# CSV to stdout
databricks query sql --warehouse-id <WAREHOUSE_ID> --sql "SELECT * FROM dim_customers" --format csv
```

Supported formats:

- `table`: human-readable tabular output (default for terminals)
- `json`: structured JSON (default when `--result-file` is specified)
- `csv`: comma-separated values

## Wait behaviour

The command waits up to 10 seconds for each statement to complete. After the
timeout it continues polling asynchronously until the statement reaches a
terminal state. Adjust the limit with `--wait-timeout`. Set the timeout to `0s`
to return immediately and poll externally using the returned statement ID.

## Read-only safety gate

By default the CLI blocks statements that may modify data or change cluster
state. The classifier removes comments, splits multi-statement files, and checks
the leading keyword of each statement. Read-only statements such as `SELECT`,
`SHOW`, `DESCRIBE`, `VALUES`, `TABLE`, `WITH … SELECT`, and `EXPLAIN SELECT`
are allowed. Statements that begin with keywords such as `ALTER`, `CREATE`,
`DELETE`, `DROP`, `INSERT`, `MERGE`, `SET`, `TRUNCATE`, `UPDATE`, `USE`, and
similar commands are rejected.

The check evaluates every statement independently. When a statement is blocked
the error identifies the keyword, statement index, and location:

```
Error: blocked by read-only policy: blocked keyword DROP at statement 1 (line 1, column 1).
```

Override the protection when you intentionally run commands with side effects:

- `--allow-destructive`
- `export DATABRICKS_CLI_ALLOW_DESTRUCTIVE_SQL=true`
- `cli.allow_destructive_sql = true` in the active profile inside `~/.databrickscfg`

Flag, environment variable, and profile are applied in that order of precedence.
