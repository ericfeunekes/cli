package query

import "github.com/spf13/cobra"

// New returns the root command for the query namespace.
func New() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "query",
		Short: "Run queries on Databricks",
		Long:  "Run SQL and other query workloads against Databricks services.",
	}

	cmd.AddCommand(newSQLCommand())
	return cmd
}
