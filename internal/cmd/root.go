package cmd

import (
	"fmt"
	"os"

	"github.com/otherjamesbrown/cobuild/internal/client"
	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/otherjamesbrown/cobuild/internal/connector"
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

CONFIGURATION:
  Uses ~/.cobuild/config.yaml and .cobuild.yaml for project/agent.
  Legacy ~/.cxp/ and .cxp.yaml paths are also supported.`,
	SilenceUsage:  true,
	SilenceErrors: true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if cmd.Name() == "version" {
			return nil
		}

		cfg, err := client.LoadClientConfig(configFlag)
		if err != nil {
			return err
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

		// Initialize work-item connector
		repoRoot := findRepoRoot()
		pCfg, _ := config.LoadConfig(repoRoot)
		conn, _ = connector.New(pCfg, cfg.Project, cfg.Agent, debugFlag)

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
