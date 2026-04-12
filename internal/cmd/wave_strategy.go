package cmd

import (
	"strconv"
	"strings"

	"github.com/otherjamesbrown/cobuild/internal/config"
	"github.com/otherjamesbrown/cobuild/internal/connector"
)

var waveStrategyOverride func() string

func resolveWaveStrategy(pCfg *config.Config) string {
	if pCfg == nil {
		pCfg = config.DefaultConfig()
	}
	switch strings.ToLower(strings.TrimSpace(pCfg.Dispatch.WaveStrategy)) {
	case "parallel":
		return "parallel"
	default:
		return "serial"
	}
}

func currentWaveStrategy() string {
	if waveStrategyOverride != nil {
		return waveStrategyOverride()
	}
	repoRoot := findRepoRoot()
	pCfg, _ := config.LoadConfig(repoRoot)
	return resolveWaveStrategy(pCfg)
}

func taskWave(item *connector.WorkItem) int {
	if item == nil || item.Metadata == nil {
		return 0
	}
	raw, ok := item.Metadata["wave"]
	if !ok {
		return 0
	}
	switch v := raw.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(v))
		if err == nil {
			return n
		}
	}
	return 0
}
