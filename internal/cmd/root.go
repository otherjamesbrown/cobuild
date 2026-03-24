package cmd

import (
	"fmt"
	"os"

	"github.com/otherjamesbrown/cobuild/internal/client"
	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/otherjamesbrown/cobuild/internal/connector"
	"github.com/otherjamesbrown/cobuild/internal/store"
	"github.com/spf13/cobra"
)

var (
	outputFormat string
	projectFlag  string
	agentFlag    string
	debugFlag    bool
	configFlag   string
	cbClient     *client.Client
	conn         connector.Connector // work-item connector
	cbStore      store.Store         // CoBuild's own data store
)

var Version = "0.1.0"

var rootCmd = &cobra.Command{
	Use:   "cobuild",
	Short: "CoBuild pipeline automation CLI",
	Long: `CoBuild — pipeline automation for turning designs into working code.

Orchestrates agents through structured pipelines with enforced stage gates.

COMMANDS:
  setup                          Register repo for pipeline automation
  poller                         Poll for triggers, spawn M sessions
  init-skills                    Copy default skills into repo
  insights                       Analyze pipeline execution data
  improve                        Suggest pipeline improvements

  init <shard-id>                Initialize pipeline on a design
  show <shard-id>                Display pipeline state
  gate <shard-id> <gate-name>    Record a gate verdict
  review <shard-id>              Phase 1 readiness review
  decompose <shard-id>           Phase 2 decomposition
  audit <shard-id>               Show pipeline audit trail
  lock/unlock/lock-check <id>    Pipeline lock management

  dispatch <task-id>             Dispatch task to agent via tmux
  complete <task-id>             Post-agent completion bookkeeping

  work-item (wi)                 Work item operations via connector
    show <id>                    Show a work item
    list                         List work items
    links <id>                   Show relationships
    status <id> <status>         Update status
    append <id> --body "..."     Append content
    create --type <t> --title    Create a work item
    label add <id> <label>       Add a label
    links add <from> <to> <type> Create a relationship

CONFIGURATION:
  Uses ~/.cobuild/config.yaml and .cobuild.yaml for project/agent.
  Legacy ~/.cxp/ and .cxp.yaml paths are also supported.`,
	SilenceUsage:  true,
	SilenceErrors: true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if cmd.Name() == "version" {
			return nil
		}

		// Legacy client — may fail if agent/user not configured.
		// New commands use the connector directly and don't need this.
		cfg, err := client.LoadClientConfig(configFlag)
		if err != nil {
			// Still try to initialize connector from pipeline config
			repoRoot := findRepoRoot()
			pCfg, _ := config.LoadConfig(repoRoot)

			// Read project from .cobuild.yaml or flags
			project := projectFlag
			if project == "" {
				project = readProjectFromYAML(repoRoot)
			}

			conn, _ = connector.New(pCfg, project, "", debugFlag)
			cbStore, _ = store.New(cmd.Context(), pCfg, "")
			return nil
		}

		if projectFlag != "" {
			cfg.Project = projectFlag
		}
		if agentFlag != "" {
			cfg.Agent = agentFlag
		}

		if cfg.Defaults != nil && cfg.Defaults.Output != "" && !cmd.Flags().Changed("output") {
			outputFormat = cfg.Defaults.Output
		}

		cbClient = client.NewClient(cfg)

		// Initialize work-item connector and store
		repoRoot := findRepoRoot()
		pCfg, _ := config.LoadConfig(repoRoot)
		conn, _ = connector.New(pCfg, cfg.Project, cfg.Agent, debugFlag)

		// Initialize CoBuild's own data store
		dsn := cbClient.ConnectionString()
		cbStore, _ = store.New(cmd.Context(), pCfg, dsn)

		return nil
	},
}

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print version information",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Printf("cobuild version %s\n", Version)
	},
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&outputFormat, "output", "o", "text", "Output format (text|json|yaml)")
	rootCmd.PersistentFlags().StringVar(&projectFlag, "project", "", "Override project from config")
	rootCmd.PersistentFlags().StringVar(&agentFlag, "agent", "", "Override agent identity")
	rootCmd.PersistentFlags().BoolVar(&debugFlag, "debug", false, "Verbose logging")
	rootCmd.PersistentFlags().StringVar(&configFlag, "config", "", "Override config file path")

	rootCmd.AddCommand(versionCmd)
}
