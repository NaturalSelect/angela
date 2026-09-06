package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
)

// recentModelsFileName is the sidecar file, stored next to a scope's
// config file, that holds recently-used models. Recording a pick
// happens on nearly every model switch, so keeping it out of the
// hand-edited config file avoids churning a file users may track in
// dotfiles.
const recentModelsFileName = "recent_models.json"

// recentModelsSidecarPath returns the recent-models file that sits next
// to configPath.
func recentModelsSidecarPath(configPath string) string {
	return filepath.Join(filepath.Dir(configPath), recentModelsFileName)
}

// decodeRecentModels parses the contents of a recent-models sidecar
// file. Any parse failure yields an empty map: the recents list is a
// convenience cache for the model picker, not data worth failing
// config load over.
func decodeRecentModels(data []byte) map[SlotName][]SelectedModel {
	if len(data) == 0 {
		return make(map[SlotName][]SelectedModel)
	}
	var rm map[SlotName][]SelectedModel
	if err := json.Unmarshal(data, &rm); err != nil || rm == nil {
		return make(map[SlotName][]SelectedModel)
	}
	return rm
}

// loadRecentModelsFile reads and decodes the recent-models sidecar at
// path, returning an empty map if it does not exist or fails to parse.
func loadRecentModelsFile(path string) map[SlotName][]SelectedModel {
	data, err := os.ReadFile(path)
	if err != nil {
		return make(map[SlotName][]SelectedModel)
	}
	return decodeRecentModels(data)
}

// saveRecentModelsFile writes rm to the recent-models sidecar at path.
func saveRecentModelsFile(path string, rm map[SlotName][]SelectedModel) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}
	data, err := json.Marshal(rm)
	if err != nil {
		return err
	}
	return atomicWriteFile(path, indentJSON(data), 0o600)
}

// loadRecentModels reads the global and, when present, workspace
// recent-models sidecars and merges them, with workspace entries
// taking priority per slot — mirroring how the workspace config file
// overrides the global one. legacy seeds the global sidecar the first
// time it is read (when no sidecar file exists yet), so upgrading from
// the old storage embedded in the config file does not drop a user's
// existing history.
func loadRecentModels(globalConfigPath, workspaceConfigPath string, legacy map[SlotName][]SelectedModel) map[SlotName][]SelectedModel {
	globalSidecar := recentModelsSidecarPath(globalConfigPath)
	merged := loadRecentModelsFile(globalSidecar)
	if _, err := os.Stat(globalSidecar); os.IsNotExist(err) && len(legacy) > 0 {
		merged = maps.Clone(legacy)
		if saveErr := saveRecentModelsFile(globalSidecar, merged); saveErr != nil {
			slog.Warn("Failed to migrate recent models to sidecar file", "error", saveErr)
		}
	}
	if workspaceConfigPath != "" {
		if ws := loadRecentModelsFile(recentModelsSidecarPath(workspaceConfigPath)); len(ws) > 0 {
			merged = maps.Clone(merged)
			maps.Copy(merged, ws)
		}
	}
	return merged
}

// recentModelsPath returns the recent-models sidecar file for scope.
func (s *ConfigStore) recentModelsPath(scope Scope) (string, error) {
	cfgPath, err := s.configPath(scope)
	if err != nil {
		return "", err
	}
	return recentModelsSidecarPath(cfgPath), nil
}

// writeRecentModels persists modelType's recent-models list to the
// sidecar file for scope, leaving other slots in that file untouched.
func (s *ConfigStore) writeRecentModels(scope Scope, modelType SlotName, list []SelectedModel) error {
	path, err := s.recentModelsPath(scope)
	if err != nil {
		return err
	}
	return s.atomicWriteAt(path, func(current []byte) ([]byte, error) {
		rm := decodeRecentModels(current)
		rm[modelType] = list
		return json.Marshal(rm)
	})
}

// RecordRecentModel adds a model to the recent-models list for the given
// type and persists it, without touching which model is selected.
//
// It is split from UpdatePreferredModel because the two answer different
// questions: "what do I run" is owned by the session's ActiveAgent, while
// "what have I picked lately" is a global list feeding the model dialog.
// Recording a recent model never changes what any session resolves to.
func (s *ConfigStore) RecordRecentModel(scope Scope, modelType SlotName, model SelectedModel) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.recordRecentModelLocked(scope, modelType, model)
}

// recordRecentModelLocked is the lock-free core of RecordRecentModel,
// also used by UpdatePreferredModel and Load's fallback-correction path
// to fold a model into the recents list. Caller must hold writeMu.
func (s *ConfigStore) recordRecentModelLocked(scope Scope, modelType SlotName, model SelectedModel) error {
	nc := s.Config().cloneForWrite()
	updated, changed := nextRecentModels(nc, modelType, model)
	if !changed {
		return nil
	}
	if err := s.writeRecentModels(scope, modelType, updated); err != nil {
		return err
	}
	if nc.RecentModels == nil {
		nc.RecentModels = make(map[SlotName][]SelectedModel)
	}
	nc.RecentModels[modelType] = updated
	s.setConfig(nc)
	return nil
}

// PruneRecentModels removes the given entries from the recent-models
// list for the given type.
//
// It takes the entries to drop rather than the list to keep. The caller
// decided what was stale from a snapshot, and between that decision and
// this write another client may have recorded a fresh pick; filtering
// the live list under writeMu preserves it, while writing back a
// precomputed list would silently erase it.
func (s *ConfigStore) PruneRecentModels(scope Scope, modelType SlotName, stale []SelectedModel) error {
	if len(stale) == 0 {
		return nil
	}
	isStale := func(recent SelectedModel) bool {
		return slices.ContainsFunc(stale, func(dead SelectedModel) bool {
			return dead.Provider == recent.Provider && dead.Model == recent.Model
		})
	}

	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	nc := s.Config().cloneForWrite()
	current := nc.RecentModels[modelType]
	kept := slices.DeleteFunc(slices.Clone(current), isStale)
	if len(kept) == len(current) {
		return nil
	}
	if err := s.writeRecentModels(scope, modelType, kept); err != nil {
		return err
	}
	nc.RecentModels[modelType] = kept
	s.setConfig(nc)
	return nil
}

// nextRecentModels computes the recent-models list for the given type
// after recording the supplied model at the front, operating on the
// provided config without persisting anything. It returns the new slice
// and whether it differs from cfg's current list. Callers fold the result
// into a clone they are about to publish.
func nextRecentModels(cfg *Config, modelType SlotName, model SelectedModel) ([]SelectedModel, bool) {
	if model.Provider == "" || model.Model == "" {
		return nil, false
	}

	eq := func(a, b SelectedModel) bool {
		return a.Provider == b.Provider && a.Model == b.Model
	}

	entry := SelectedModel{
		Provider: model.Provider,
		Model:    model.Model,
	}

	current := cfg.RecentModels[modelType]
	withoutCurrent := slices.DeleteFunc(slices.Clone(current), func(existing SelectedModel) bool {
		return eq(existing, entry)
	})

	updated := append([]SelectedModel{entry}, withoutCurrent...)
	if len(updated) > maxRecentModelsPerType {
		updated = updated[:maxRecentModelsPerType]
	}

	if slices.EqualFunc(current, updated, eq) {
		return current, false
	}

	return updated, true
}
