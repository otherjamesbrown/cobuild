package harness

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/otherjamesbrown/cobuild/internal/connector"
)

type fakeConnectorState struct {
	NextID   int                           `json:"next_id"`
	Items    map[string]connector.WorkItem `json:"items"`
	Incoming map[string][]connector.Edge   `json:"incoming"`
	Outgoing map[string][]connector.Edge   `json:"outgoing"`
}

func (h *Harness) AddWorkItem(item connector.WorkItem) error {
	return h.withFakeState(true, func(state *fakeConnectorState) error {
		if strings.TrimSpace(item.ID) == "" {
			return fmt.Errorf("fake connector item id is required")
		}
		if item.CreatedAt.IsZero() {
			item.CreatedAt = time.Now().UTC()
		}
		if item.UpdatedAt.IsZero() {
			item.UpdatedAt = item.CreatedAt
		}
		item.Metadata = cloneMetadata(item.Metadata)
		item.Labels = append([]string(nil), item.Labels...)
		item.Edges = nil
		state.Items[item.ID] = item
		return nil
	})
}

func (h *Harness) AddWorkItemEdge(fromID, toID, edgeType string) error {
	return h.withFakeState(true, func(state *fakeConnectorState) error {
		return fakeStateCreateEdge(state, fromID, toID, edgeType)
	})
}

func (h *Harness) ListWorkItems() ([]connector.WorkItem, error) {
	items := []connector.WorkItem{}
	err := h.withFakeState(false, func(state *fakeConnectorState) error {
		for _, item := range state.Items {
			items = append(items, cloneWorkItem(item))
		}
		sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
		return nil
	})
	return items, err
}

func (h *Harness) withFakeState(write bool, fn func(state *fakeConnectorState) error) error {
	lockPath := h.ConnectorState + ".lock"
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("open fake connector lock: %w", err)
	}
	defer lockFile.Close()

	lockType := syscall.LOCK_SH
	if write {
		lockType = syscall.LOCK_EX
	}
	if err := syscall.Flock(int(lockFile.Fd()), lockType); err != nil {
		return fmt.Errorf("lock fake connector state: %w", err)
	}
	defer syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)

	state, err := h.readFakeState()
	if err != nil {
		return err
	}
	if err := fn(state); err != nil {
		return err
	}
	if !write {
		return nil
	}
	return h.writeFakeState(state)
}

func (h *Harness) readFakeState() (*fakeConnectorState, error) {
	if err := os.MkdirAll(filepath.Dir(h.ConnectorState), 0o755); err != nil {
		return nil, fmt.Errorf("mkdir fake state dir: %w", err)
	}
	if _, err := os.Stat(h.ConnectorState); os.IsNotExist(err) {
		state := &fakeConnectorState{
			Items:    map[string]connector.WorkItem{},
			Incoming: map[string][]connector.Edge{},
			Outgoing: map[string][]connector.Edge{},
		}
		if err := h.writeFakeState(state); err != nil {
			return nil, err
		}
		return state, nil
	}
	data, err := os.ReadFile(h.ConnectorState)
	if err != nil {
		return nil, fmt.Errorf("read fake connector state: %w", err)
	}
	state := &fakeConnectorState{}
	if len(strings.TrimSpace(string(data))) != 0 {
		if err := json.Unmarshal(data, state); err != nil {
			return nil, fmt.Errorf("parse fake connector state: %w", err)
		}
	}
	if state.Items == nil {
		state.Items = map[string]connector.WorkItem{}
	}
	if state.Incoming == nil {
		state.Incoming = map[string][]connector.Edge{}
	}
	if state.Outgoing == nil {
		state.Outgoing = map[string][]connector.Edge{}
	}
	return state, nil
}

func (h *Harness) writeFakeState(state *fakeConnectorState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal fake connector state: %w", err)
	}
	tmp := h.ConnectorState + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write fake connector temp state: %w", err)
	}
	if err := os.Rename(tmp, h.ConnectorState); err != nil {
		return fmt.Errorf("rename fake connector state: %w", err)
	}
	return nil
}

func fakeStateCreateEdge(state *fakeConnectorState, fromID, toID, edgeType string) error {
	from, ok := state.Items[fromID]
	if !ok {
		return fmt.Errorf("fake connector: from item %q not found", fromID)
	}
	to, ok := state.Items[toID]
	if !ok {
		return fmt.Errorf("fake connector: to item %q not found", toID)
	}
	outgoing := connector.Edge{Direction: "outgoing", EdgeType: edgeType, ItemID: toID, Title: to.Title, Type: to.Type, Status: to.Status}
	incoming := connector.Edge{Direction: "incoming", EdgeType: edgeType, ItemID: fromID, Title: from.Title, Type: from.Type, Status: from.Status}
	state.Outgoing[fromID] = upsertEdge(state.Outgoing[fromID], outgoing)
	state.Incoming[toID] = upsertEdge(state.Incoming[toID], incoming)
	return nil
}
