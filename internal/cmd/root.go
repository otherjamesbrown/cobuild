package cmd

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/otherjamesbrown/cobuild/internal/cliutil"
	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/otherjamesbrown/cobuild/internal/connector"
	pipelinestate "github.com/otherjamesbrown/cobuild/internal/pipeline/state"
	"github.com/otherjamesbrown/cobuild/internal/store"
	"github.com/spf13/cobra"
)

var (
	outputFormat string
	projectFlag  string
	agentFlag    string
	debugFlag    bool
	configFlag   string

	// Core globals — initialized in PersistentPreRunE
	projectName   string              // from .cobuild.yaml or flags
	projectPrefix string              // from .cobuild.yaml (e.g., "cb-")
	conn          connector.Connector // work-item connector
	cbStore       store.Store         // CoBuild's own data store
	storeDSN      string              // Postgres DSN for commands that need raw pgx (dashboard, admin tooling)
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
  orchestrate <shard-id>         Run a pipeline in the foreground
  show <shard-id>                Display pipeline state
  gate <shard-id> <gate-name>    Record a gate verdict
  review <shard-id>              Phase 1 readiness review
  decompose <shard-id>           Phase 2 decomposition
  audit <shard-id>               Show pipeline audit trail

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
  Uses ~/.cobuild/config.yaml (global) and .cobuild.yaml (per-repo).
  COBUILD_HOST / COBUILD_DATABASE / COBUILD_USER / COBUILD_SSLMODE env
  vars override the config file. No legacy ~/.cxp/ fallback.`,
	SilenceUsage:  true,
	SilenceErrors: true,
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		if cmd.Name() == "version" {
			return nil
		}

		// Load project identity from .cobuild.yaml
		repoRoot := findRepoRoot()
		projYAML := readProjectConfigFromYAML(repoRoot)
		projectName = projYAML.Project
		projectPrefix = projYAML.Prefix
		if projectFlag != "" {
			projectName = projectFlag
		}

		// Load pipeline config — when --project targets a different project,
		// load that project's config so the correct connector is used.
		configRoot := repoRoot
		if projectFlag != "" && projectFlag != projYAML.Project {
			if projRoot, err := config.RepoForProject(projectFlag); err == nil {
				configRoot = projRoot
			}
		}
		// Surface LoadConfig errors on stderr rather than silently falling
		// back to defaults (cb-663873). LoadConfig already distinguishes
		// "file not found" (returns nil, nil) from malformed YAML (returns
		// error); the nil error path is still quiet.
		pCfg, err := config.LoadConfig(configRoot)
		if err != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "Warning: pipeline config load failed: %v\n", err)
		}

		// Fall back to deriving projectName from other sources when
		// .cobuild.yaml doesn't exist. Fixes cb-11a464 where
		// `cobuild update-agents` produced an empty **Name:** field for
		// repos that were set up via `cobuild setup` (which historically
		// didn't write .cobuild.yaml). Try in order of specificity:
		//   1. ~/.cobuild/repos.yaml entry whose path matches repoRoot
		//   2. github.owner_repo basename from the pipeline config
		//   3. directory basename of repoRoot
		if projectName == "" && repoRoot != "" {
			if reg, err := config.LoadRepoRegistry(); err == nil {
				for name, entry := range reg.Repos {
					if entry.Path == repoRoot {
						projectName = name
						break
					}
				}
			}
			if projectName == "" && pCfg != nil && pCfg.GitHub.OwnerRepo != "" {
				_, repo, ok := strings.Cut(pCfg.GitHub.OwnerRepo, "/")
				if ok && repo != "" {
					projectName = repo
				}
			}
			if projectName == "" {
				projectName = filepath.Base(repoRoot)
			}
		}

		// Initialize connector (always — needed for wi commands). Surface
		// init errors on stderr (cb-663873); commands that need the
		// connector still fail at the conn==nil check site, but the
		// operator now knows WHY.
		agent := agentFlag
		var connErr error
		conn, connErr = connector.New(pCfg, projectName, agent, debugFlag)
		if connErr != nil {
			fmt.Fprintf(cmd.ErrOrStderr(), "Warning: connector init failed: %v\n", connErr)
		}

		// Initialize store from ~/.cobuild/config.yaml (or --config override).
		// Empty DSN / missing config means no store; commands that need one
		// fail cleanly when they try to use it (cb-3f5be6 / cb-b2f3ac —
		// legacy internal/client and its dev02.brown.chat default are gone).
		// Surface load/init errors on stderr so missing config is a visible
		// signal rather than silent-degrade behaviour (cb-663873).
		storeCfg, storeCfgErr := cliutil.LoadStoreConfig(configFlag)
		if storeCfgErr != nil {
			// Silent for commands that don't need a store. Distinguishing
			// which ones do is harder than it looks (wi create doesn't,
			// init does); rather than maintain that list, let callers hit
			// cbStore==nil and print their own reason. Uncomment the next
			// line during local debugging if a store-requiring command
			// fails mysteriously.
			// fmt.Fprintf(cmd.ErrOrStderr(), "Note: store config load: %v\n", storeCfgErr)
		} else {
			if storeCfg.Defaults != nil && storeCfg.Defaults.Output != "" && !cmd.Flags().Changed("output") {
				outputFormat = storeCfg.Defaults.Output
			}
			storeDSN = storeCfg.DSN()
			var storeErr error
			cbStore, storeErr = store.New(cmd.Context(), pCfg, storeDSN)
			if storeErr != nil {
				fmt.Fprintf(cmd.ErrOrStderr(), "Warning: store init failed: %v\n", storeErr)
			}
		}

		pipelinestate.ConfigureDefault(pipelinestate.Dependencies{
			Connector: conn,
			Store:     cbStore,
			Exec:      tmuxCommandRunner(pCfg),
		})

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
		if shouldPrintCommandError(err) {
			fmt.Fprintln(os.Stderr, err)
		}
		os.Exit(commandExitCode(err))
	}
}

type commandExitCoder interface {
	ExitCode() int
}

type commandExitError struct {
	err   error
	code  int
	print bool
}

func (e *commandExitError) Error() string {
	if e == nil || e.err == nil {
		return ""
	}
	return e.err.Error()
}

func (e *commandExitError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.err
}

func (e *commandExitError) ExitCode() int {
	if e == nil || e.code == 0 {
		return 1
	}
	return e.code
}

func commandErrorWithExitCode(err error, code int) error {
	return commandErrorWithExitCodeAndPrint(err, code, true)
}

func commandErrorWithExitCodeAndPrint(err error, code int, print bool) error {
	if err == nil {
		return nil
	}
	return &commandExitError{err: err, code: code, print: print}
}

func commandExitCode(err error) int {
	if err == nil {
		return 0
	}
	var coder commandExitCoder
	if errors.As(err, &coder) {
		return coder.ExitCode()
	}
	return 1
}

func shouldPrintCommandError(err error) bool {
	if err == nil {
		return false
	}
	var exitErr *commandExitError
	if errors.As(err, &exitErr) {
		return exitErr.print
	}
	return true
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&outputFormat, "output", "o", "text", "Output format (text|json|yaml)")
	rootCmd.PersistentFlags().StringVar(&projectFlag, "project", "", "Override project from config")
	rootCmd.PersistentFlags().StringVar(&agentFlag, "agent", "", "Override agent identity")
	rootCmd.PersistentFlags().BoolVar(&debugFlag, "debug", false, "Verbose logging")
	rootCmd.PersistentFlags().StringVar(&configFlag, "config", "", "Override config file path")

	rootCmd.AddCommand(versionCmd)
}
