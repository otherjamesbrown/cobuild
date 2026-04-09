package connector

import (
	"fmt"

	"github.com/otherjamesbrown/cobuild/internal/config"
)

// New creates a Connector from pipeline config and client settings.
// If no connector is configured, defaults to context-palace.
func New(cfg *config.Config, project, agent string, debug bool) (Connector, error) {
	connType := "context-palace"
	var connCfg map[string]string

	if cfg != nil && cfg.Connectors.WorkItems.Type != "" {
		connType = cfg.Connectors.WorkItems.Type
		connCfg = cfg.Connectors.WorkItems.Config
	}

	switch connType {
	case "context-palace", "cp":
		return NewCPConnector(project, agent, debug), nil

	case "beads", "bd":
		prefix := "bd"
		repo := ""
		if connCfg != nil {
			if p, ok := connCfg["prefix"]; ok {
				prefix = p
			}
			if r, ok := connCfg["repo"]; ok {
				repo = r
			}
		}
		// Default repo path from the registry if not explicitly configured
		if repo == "" {
			if repoPath, err := config.RepoForProject(project); err == nil {
				repo = repoPath
			}
		}
		return NewBeadsConnector(prefix, repo, debug), nil

	default:
		return nil, fmt.Errorf("unknown connector type: %q (supported: context-palace, beads)", connType)
	}
}
