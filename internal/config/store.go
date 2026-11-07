package config

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"time"

	"charm.land/catwalk/pkg/catwalk"
	"github.com/NaturalSelect/angela/internal/env"
	"github.com/NaturalSelect/angela/internal/lock"
	"github.com/NaturalSelect/angela/internal/oauth"
	"github.com/NaturalSelect/angela/internal/permission"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"golang.org/x/sync/singleflight"
)

// configLockDeadline bounds how long lockPath waits for the
// cross-process flock before giving up. A few seconds is plenty for
// honest contention; longer suggests something is wedged.
const configLockDeadline = 5 * time.Second

// refreshLockDeadline bounds how long RefreshOAuthToken waits for the
// per-provider cross-process refresh lock. It must exceed the token
// exchange HTTP timeout (30s) so that a peer mid-exchange is given time
// to finish and publish its result, which we then adopt instead of
// running our own exchange. Running our own would reuse an
// already-rotated refresh token and trip the provider's reuse detection,
// revoking the whole token family.
const refreshLockDeadline = 45 * time.Second

// credentialWriteLockDeadline bounds how long a credential write (e.g.
// storing the token from a fresh interactive login) waits for the
// per-provider refresh lock. It is deliberately shorter than
// refreshLockDeadline because a user is watching: if a peer is wedged we
// would rather write and risk a rare clobber than hang the UI.
const credentialWriteLockDeadline = 10 * time.Second

// RuntimeOverrides holds per-session settings that are never persisted to
// disk. They are applied on top of the loaded Config and survive only for
// the lifetime of the process (or workspace).
type RuntimeOverrides struct {
	// PermissionMode is the runtime permission mode (manual, auto-accept
	// edits, or yolo). It defaults to permission.ModeManual.
	PermissionMode permission.PermissionMode
	// EnabledChannels lists the MCP servers opted in as channels for this
	// session (via the --channels flag). A server present in MCP config only
	// pushes channel events when it also appears here. Entries may be written
	// as "server:<name>" or as a bare "<name>".
	EnabledChannels []string
	// Slots records the model choices made in this instance, whether
	// persisted or not. They are reapplied after a config reload so that a
	// selection made here always outranks whatever the shared config file
	// happens to hold — see pinPreferredModelLocked.
	Slots map[SlotName]SelectedModel
	// NoDockerSandbox disables the shortcut that treats an existing
	// Docker/OCI container as sufficient sandboxing (via the
	// --no-docker-sandbox flag), so sandbox.New still applies Landlock
	// enforcement even when the process is already running inside a
	// container.
	NoDockerSandbox bool
	// YoloMerge enables the shortcut that lets yolo mode skip the
	// merge tool's approval prompt (via the --yolo-merge flag), so
	// merging a branch is auto-approved in yolo mode.
	YoloMerge bool
	// NoVSCodeDiff disables the VS Code MCP diff-review channel (via
	// the --no-vscode-diff flag), even when a matching VS Code window
	// is detected, so an edit-shaped prompt only ever opens in the
	// terminal.
	NoVSCodeDiff bool
	// EnableImageTools is set by the --enable-image-tools flag. Nil means
	// the flag was not passed, so options.enable_image_tools applies.
	EnableImageTools *bool
	// NonInteractive starts the coder agent without interactive-only tools
	// such as question, for front ends that cannot ask the user anything.
	NonInteractive bool
	// NoBranchAgents keeps branch agents and background agents off even
	// when the session is interactive, for front ends that cannot hand a
	// conversation over to a sub-session.
	NoBranchAgents bool
	// Env, when set, is an environment snapshot as KEY=VALUE strings that
	// checks like TERM_PROGRAM read instead of this process's own. It is
	// nil in normal use, where os.Getenv already reflects the user's
	// terminal.
	Env []string
	// MCP holds servers a front end registered for this process only, such
	// as the ones an ACP editor passes with a session request. They are
	// merged into the config after every reload and never written to disk.
	// Only AddRuntimeMCP and reload touch it, both under writeMu.
	MCP map[string]MCPConfig
}

// ConfigStore is the single entry point for all config access. It owns the
// pure-data Config, runtime state (working directory, resolver, known
// providers), and persistence to both global and workspace config files.
//
// mu serialises all config file mutations (SetConfigFields,
// RemoveConfigField, RefreshOAuthToken) to prevent both in-process
// goroutine races and, together with the shared lock.File, cross-process
// races on the config file.
//
// writeMu serialises every operation that produces a new in-memory Config:
// the typed copy-on-write mutators (SetCompactMode, UpdatePreferredModel,
// ...) and ReloadFromDisk. Typed mutators take Lock; autoReload takes
// TryLock so a write triggered re-entrantly during a reload (e.g.
// configureProviders calling RemoveConfigField) skips the nested reload
// instead of deadlocking. This is what lets published Configs be treated
// as immutable: a mutator clones, mutates the clone, and swaps it in under
// writeMu rather than mutating the live Config in place.
type ConfigStore struct {
	config             *Config
	workingDir         string
	resolver           VariableResolver
	envOnlyResolver    VariableResolver // env-var-only fallback for untrusted provider/MCP data fields; see dataFieldTrust
	dataTrust          *dataFieldTrust
	globalDataPath     string   // ~/.config/angela/angela.json
	workspacePath      string   // .angela/angela.json
	loadedPaths        []string // config files that were successfully loaded
	knownProviders     []catwalk.Provider
	overrides          RuntimeOverrides
	trackedConfigPaths []string                // unique, normalized config file paths
	snapshots          map[string]fileSnapshot // path -> snapshot at last capture

	// configMu guards the config pointer field against concurrent
	// readers (Config) and the writeMu-serialised swap (setConfig). It
	// protects the pointer word only; the pointed-to Config is treated
	// as immutable once published, since both reloads and typed mutators
	// build a fresh Config rather than mutating the live one.
	configMu sync.RWMutex

	mu      sync.Mutex   // serialises config file writes
	writeMu sync.RWMutex // serialises in-memory config production (mutators + reload); RLock for readers

	// refreshSF collapses concurrent in-process OAuth refreshes for the
	// same provider into a single attempt. Combined with the per-provider
	// cross-process refresh lock, it ensures only one token exchange runs
	// at a time. See RefreshOAuthToken.
	refreshSF singleflight.Group

	// exchangeToken performs the provider-specific OAuth token exchange.
	// It is a field so tests can substitute a fake exchange without making
	// real network calls. Production code leaves it nil, and exchange falls
	// back to the real provider clients.
	exchangeToken func(ctx context.Context, providerID, refreshToken string) (*oauth.Token, error)

	// authSignals tracks interactive re-authentication completions. Its
	// methods are promoted onto ConfigStore, and the zero value is ready
	// to use.
	authSignals
}

// Config returns the pure-data config struct (read-only after load).
//
// The pointer read is guarded by configMu so it can never tear against
// the reload swap in reloadFromDiskLocked. Reloads build a brand-new
// Config and swap it in rather than mutating the live one, so holding the
// returned pointer stays safe even across a concurrent reload — the reader
// keeps reading its (now immutable) snapshot.
func (s *ConfigStore) Config() *Config {
	s.configMu.RLock()
	defer s.configMu.RUnlock()
	return s.config
}

// setConfig atomically swaps the active config pointer under configMu.
// Used by the reload path; in-place field mutators leave the pointer
// untouched and run under mu instead.
func (s *ConfigStore) setConfig(cfg *Config) {
	s.configMu.Lock()
	defer s.configMu.Unlock()
	s.config = cfg
}

// WorkingDir returns the current working directory.
func (s *ConfigStore) WorkingDir() string {
	return s.workingDir
}

// Resolver returns the variable resolver.
func (s *ConfigStore) Resolver() VariableResolver {
	s.writeMu.RLock()
	defer s.writeMu.RUnlock()
	return s.resolver
}

// Resolve resolves a variable reference using the configured resolver.
func (s *ConfigStore) Resolve(key string) (string, error) {
	s.writeMu.RLock()
	r := s.resolver
	s.writeMu.RUnlock()
	if r == nil {
		return "", fmt.Errorf("no variable resolver configured")
	}
	return r.ResolveValue(key)
}

// ProviderFieldResolver returns the resolver to use for a provider
// data field (api_key, base_url, or an extra_headers value): the full
// shell resolver, unless field was last set by an untrusted (i.e. not
// system or global) config layer, in which case command substitution
// is disabled and only $VAR/${VAR} expands. See dataFieldTrust.
func (s *ConfigStore) ProviderFieldResolver(providerID, field string) VariableResolver {
	s.writeMu.RLock()
	defer s.writeMu.RUnlock()
	if s.dataTrust.providerUntrusted(providerID, field) {
		return s.envOnlyResolver
	}
	return s.resolver
}

// MCPFieldResolver returns the resolver to use for an MCP data field
// (url, a headers value, oauth_client_id, or oauth_client_secret). See
// ProviderFieldResolver.
func (s *ConfigStore) MCPFieldResolver(name, field string) VariableResolver {
	s.writeMu.RLock()
	defer s.writeMu.RUnlock()
	if _, ok := s.overrides.MCP[name]; ok {
		return IdentityResolver()
	}
	if s.dataTrust.mcpUntrusted(name, field) {
		return s.envOnlyResolver
	}
	return s.resolver
}

// KnownProviders returns the list of known providers.
func (s *ConfigStore) KnownProviders() []catwalk.Provider {
	s.writeMu.RLock()
	defer s.writeMu.RUnlock()
	return s.knownProviders
}

// SetupAgents resolves the agent set from the live config and
// publishes it as a new Config value, so concurrent readers of
// Config() never observe an in-place mutation of Agents.
func (s *ConfigStore) SetupAgents() {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.setupAgentsLocked()
}

// setupAgentsLocked is the lock-free core of SetupAgents. Caller must
// hold writeMu.
func (s *ConfigStore) setupAgentsLocked() {
	nc := s.Config().cloneForWrite()
	prepareResolvedConfig(nc)
	s.setConfig(nc)
}

// Overrides returns the runtime overrides for this store.
func (s *ConfigStore) Overrides() *RuntimeOverrides {
	s.writeMu.RLock()
	defer s.writeMu.RUnlock()
	return &s.overrides
}

// ImageToolsEnabled returns the effective tri-state image tools
// setting: the --enable-image-tools override when passed, else
// options.enable_image_tools. Nil means "auto": enable the tools only
// once the client reports image rendering support.
func (s *ConfigStore) ImageToolsEnabled() *bool {
	if enabled := s.Overrides().EnableImageTools; enabled != nil {
		return enabled
	}
	if cfg := s.Config(); cfg != nil && cfg.Options != nil {
		return cfg.Options.EnableImageTools
	}
	return nil
}

// LoadedPaths returns the config file paths that were successfully loaded.
func (s *ConfigStore) LoadedPaths() []string {
	s.writeMu.RLock()
	defer s.writeMu.RUnlock()
	return slices.Clone(s.loadedPaths)
}

// lockPath acquires both the in-process mutex and a cross-process flock
// on the given file. It also guards sidecar files (see recent_models.go)
// that live outside the scoped config files. Callers that need to do I/O
// between reading and writing (e.g. an HTTP token exchange) must use
// lockPath explicitly rather than atomicWrite.
//
// The returned release function drops both locks. Callers must call it
// as soon as the file access is complete — no I/O should be performed
// while the lock is held.
func (s *ConfigStore) lockPath(path string) (func(), error) {
	s.mu.Lock()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		s.mu.Unlock()
		return nil, fmt.Errorf("create config directory: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), configLockDeadline)
	defer cancel()
	release, err := lock.File(ctx, path+".lock")
	if err != nil {
		s.mu.Unlock()
		return nil, fmt.Errorf("acquire config lock: %w", err)
	}
	return func() {
		release()
		s.mu.Unlock()
	}, nil
}

// atomicWrite handles the lock-read-transform-write-unlock cycle for
// config file mutations. The fn callback receives the current file
// contents (raw bytes, or {} if the file is missing) and must return the
// new contents. fn must be pure — no I/O, no network calls.
func (s *ConfigStore) atomicWrite(scope Scope, fn func(current []byte) ([]byte, error)) error {
	path, err := s.configPath(scope)
	if err != nil {
		return err
	}
	return s.atomicWriteAt(path, fn)
}

// atomicWriteAt is the path-based core of atomicWrite, also used to
// persist sidecar files (see recent_models.go) that live outside the
// scoped config files.
func (s *ConfigStore) atomicWriteAt(path string, fn func(current []byte) ([]byte, error)) error {
	unlock, err := s.lockPath(path)
	if err != nil {
		return err
	}
	defer unlock()

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			data = []byte("{}")
		} else {
			return fmt.Errorf("read config file: %w", err)
		}
	}

	newData, err := fn(data)
	if err != nil {
		return err
	}

	return atomicWriteFile(path, indentJSON(newData), 0o600)
}

// indentJSON pretty-prints config JSON so the file stays readable and
// hand-editable. sjson emits compact output, which collapses the entire
// config onto a single line. Content that does not parse is written
// through unchanged rather than dropped.
func indentJSON(data []byte) []byte {
	var buf bytes.Buffer
	if err := json.Indent(&buf, data, "", "  "); err != nil {
		return data
	}
	return append(buf.Bytes(), '\n')
}

// configPath returns the file path for the given scope.
func (s *ConfigStore) configPath(scope Scope) (string, error) {
	switch scope {
	case ScopeWorkspace:
		if s.workspacePath == "" {
			return "", ErrNoWorkspaceConfig
		}
		return s.workspacePath, nil
	default:
		return s.globalDataPath, nil
	}
}

// HasConfigField checks whether a key exists in the config file for the given
// scope.
func (s *ConfigStore) HasConfigField(scope Scope, key string) bool {
	path, err := s.configPath(scope)
	if err != nil {
		return false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return gjson.Get(string(data), key).Exists()
}

// SetConfigField sets a key/value pair in the config file for the given scope.
// After a successful write, it automatically reloads config to keep in-memory
// state fresh.
func (s *ConfigStore) SetConfigField(scope Scope, key string, value any) error {
	return s.SetConfigFields(scope, map[string]any{key: value})
}

// SetConfigFields sets multiple key/value pairs in the config file for the
// given scope in a single write, then reloads in-memory state from disk.
//
// Use this for arbitrary external edits where the in-memory effect of the
// change is not known ahead of time. The typed mutators (which know exactly
// what changed) go through update instead and skip the reload.
//
// The write is protected by an in-process mutex and a cross-process flock
// to prevent races between concurrent writers in different processes.
func (s *ConfigStore) SetConfigFields(scope Scope, kv map[string]any) error {
	if err := s.writeConfigFields(scope, kv); err != nil {
		return err
	}
	// Auto-reload to keep in-memory state fresh after config edits.
	// We use context.Background() since this is an internal operation that
	// shouldn't be cancelled by user context.
	if err := s.autoReload(context.Background()); err != nil {
		// Log warning but don't fail the write - disk is already updated.
		slog.Warn("Config file updated but failed to reload in-memory state", "error", err)
	}
	return nil
}

// writeConfigFields persists key/value pairs to the config file. It does not
// touch in-memory config state or the staleness snapshot: callers either
// reload (SetConfigFields, whose reload recaptures the snapshot) or have
// already published an updated clone and capture the snapshot themselves
// (update). Both of those run under writeMu, which is what keeps the
// snapshot map free of concurrent writers.
func (s *ConfigStore) writeConfigFields(scope Scope, kv map[string]any) error {
	// Sort keys for deterministic output regardless of map iteration
	// order. This also ensures consistent results when callers pass
	// overlapping JSONPath keys (e.g. "a" and "a.b").
	keys := make([]string, 0, len(kv))
	for k := range kv {
		keys = append(keys, k)
	}
	slices.Sort(keys)

	return s.atomicWrite(scope, func(data []byte) ([]byte, error) {
		v := string(data)
		for _, key := range keys {
			var sErr error
			if v, sErr = sjson.Set(v, key, kv[key]); sErr != nil {
				return nil, fmt.Errorf("failed to set config field %s: %w", key, sErr)
			}
		}
		return []byte(v), nil
	})
}

// mutateInMemory applies a copy-on-write change to the config without
// persisting. Under writeMu it clones the live config, lets mutate edit the
// clone, and publishes it. This is the single primitive every in-memory
// config change goes through, so a published Config is never mutated in
// place and readers always see a consistent snapshot.
func (s *ConfigStore) mutateInMemory(mutate func(*Config)) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	nc := s.Config().cloneForWrite()
	mutate(nc)
	s.setConfig(nc)
}

// update applies a copy-on-write change and persists the reported fields.
// mutate edits the clone and returns the JSON-path fields to write to disk;
// because the clone already reflects the change, no reload is needed.
// Returning an empty map publishes the clone without a disk write.
func (s *ConfigStore) update(scope Scope, mutate func(*Config) map[string]any) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.updateLocked(scope, mutate)
}

// updateLocked is the lock-free core of update. Caller must hold writeMu.
func (s *ConfigStore) updateLocked(scope Scope, mutate func(*Config) map[string]any) error {
	nc := s.Config().cloneForWrite()
	// mutate may also pin an instance-local model override. Both that
	// pin and the new snapshot must stay invisible until the write
	// lands, or a failed write leaves the process running on a
	// configuration that is not on disk and that a later reload undoes.
	pinned := maps.Clone(s.overrides.Slots)
	fields := mutate(nc)
	if len(fields) == 0 {
		s.setConfig(nc)
		return nil
	}
	if err := s.writeConfigFields(scope, fields); err != nil {
		s.overrides.Slots = pinned
		return err
	}
	s.setConfig(nc)
	// Refresh the staleness snapshot so the file watcher does not treat
	// our own write as an external change. Safe to touch the snapshot map
	// here because we hold writeMu.
	if path, err := s.configPath(scope); err == nil {
		s.captureStalenessSnapshot(append(slices.Clone(s.loadedPaths), path))
	}
	return nil
}

// OverridePreferredModel sets the preferred model for the given type in
// memory only, without persisting.
//
// It backs the non-interactive --small-model flag and, through
// UpdatePreferredModel's ScopeEphemeral, a model picked from the model
// dialog before any session exists. The chore model also serves the
// internal agents (titling, compaction), which instantiate from config
// rather than from any session and so have no ActiveAgent of their own
// to edit. Everything session-scoped goes through
// Coordinator.EditActiveAgent instead: overriding SlotMain here
// would change the model for every session in the process.
func (s *ConfigStore) OverridePreferredModel(modelType SlotName, model SelectedModel) {
	s.mutateInMemory(func(c *Config) {
		if c.Slots == nil {
			c.Slots = make(map[SlotName]SelectedModel)
		}
		c.Slots[modelType] = model
		s.pinPreferredModelLocked(modelType, model)
	})
}

// pinPreferredModelLocked records a model choice made in this instance so
// that a later config reload cannot replace it with a choice made
// somewhere else. Several Angela instances share one global config file, so
// a reload triggered by an unrelated write (a token refresh, say) would
// otherwise import whichever model a sibling instance last selected and
// switch models out from under the user mid-session.
//
// Caller must hold writeMu.
func (s *ConfigStore) pinPreferredModelLocked(modelType SlotName, model SelectedModel) {
	if s.overrides.Slots == nil {
		s.overrides.Slots = make(map[SlotName]SelectedModel)
	}
	s.overrides.Slots[modelType] = model
}

// RemoveConfigField removes a key from the config file for the given scope.
// After a successful write, it automatically reloads config to keep in-memory
// state fresh.
//
// The write is protected by an in-process mutex and a cross-process flock.
func (s *ConfigStore) RemoveConfigField(scope Scope, key string) error {
	err := s.atomicWrite(scope, func(data []byte) ([]byte, error) {
		v, sErr := sjson.Delete(string(data), key)
		if sErr != nil {
			return nil, fmt.Errorf("failed to delete config field %s: %w", key, sErr)
		}
		return []byte(v), nil
	})
	if err != nil {
		return err
	}

	if err := s.autoReload(context.Background()); err != nil {
		slog.Warn("Config file updated but failed to reload in-memory state", "error", err)
	}

	return nil
}

// UpdatePreferredModel updates the preferred model for the given type and
// persists it to the config file at the given scope. The selected model and
// the recent-models list are written together in a single config write.
//
// ScopeEphemeral skips the config file and applies in memory only, like
// OverridePreferredModel: the model dialog uses it for a pick made
// before any session exists, where there is nothing to scope the
// choice to and writing it to disk would silently replace the saved
// default the user never meant to change.
//
// The write skips the full disk reparse/reload (which would rebuild the
// provider catalog and agents on every model switch and dominate selection
// latency); agents are refreshed separately by the caller (see
// UpdateAgentModel).
func (s *ConfigStore) UpdatePreferredModel(scope Scope, modelType SlotName, model SelectedModel) error {
	if scope == ScopeEphemeral {
		s.OverridePreferredModel(modelType, model)
		return nil
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.updatePreferredModelLocked(scope, modelType, model)
}

// updatePreferredModelLocked is the lock-free core of UpdatePreferredModel
// for non-ephemeral scopes, also used by Load's fallback-correction path.
// It folds the pick into the recents list first (see recent_models.go),
// matching what a direct RecordRecentModel call would do, then persists
// the selection itself. Caller must hold writeMu.
func (s *ConfigStore) updatePreferredModelLocked(scope Scope, modelType SlotName, model SelectedModel) error {
	if err := s.recordRecentModelLocked(scope, modelType, model); err != nil {
		return err
	}
	return s.updateLocked(scope, func(c *Config) map[string]any {
		return s.updatePreferredModelFields(c, modelType, model)
	})
}

// UpsertProviderModel records a model under a provider's model list in
// the config file, replacing the entry with the same ID when one is
// already there.
//
// It reads the array back out of the file rather than off the in-memory
// ProviderConfig: the loader merges the whole catalog into that one, so
// writing it back would freeze hundreds of catalog models into the
// user's config and stop them tracking catalog updates.
//
// The provider must already exist in the active configuration or in the
// known provider catalog. Writing a model for an unknown provider would
// leave an unusable fragment in the config file because
// configureProviders removes providers that lack an endpoint.
func (s *ConfigStore) UpsertProviderModel(scope Scope, providerID string, model ProviderModel) error {
	if providerID == "" || model.ID == "" {
		return fmt.Errorf("provider id and model id are required")
	}

	if !s.isKnownProvider(providerID) {
		return fmt.Errorf("provider %q is not configured or known", providerID)
	}

	key := fmt.Sprintf("providers.%s.models", escapePathKey(providerID))
	err := s.atomicWrite(scope, func(data []byte) ([]byte, error) {
		var models []ProviderModel
		if raw := gjson.Get(string(data), key); raw.Exists() {
			if uErr := json.Unmarshal([]byte(raw.Raw), &models); uErr != nil {
				return nil, fmt.Errorf("failed to read models for provider %s: %w", providerID, uErr)
			}
		}

		if i := slices.IndexFunc(models, func(m ProviderModel) bool { return m.ID == model.ID }); i >= 0 {
			models[i] = model
		} else {
			models = append(models, model)
		}

		v, sErr := sjson.Set(string(data), key, models)
		if sErr != nil {
			return nil, fmt.Errorf("failed to write models for provider %s: %w", providerID, sErr)
		}
		return []byte(v), nil
	})
	if err != nil {
		return err
	}

	// Use a blocking reload instead of autoReload. autoReload skips
	// when another goroutine holds writeMu, which can leave the
	// in-memory config stale — the model is persisted but unresolved.
	// Callers like onboarding depend on the model being available
	// immediately for UpdatePreferredModel and InitCoderAgent.
	if err := s.ReloadFromDisk(context.Background()); err != nil {
		slog.Warn("Config file updated but failed to reload in-memory state", "error", err)
	}

	return nil
}

// isKnownProvider reports whether providerID exists in the active
// configuration or in the known provider catalog.
func (s *ConfigStore) isKnownProvider(providerID string) bool {
	s.writeMu.RLock()
	defer s.writeMu.RUnlock()

	// Check active config providers.
	if s.config != nil {
		if _, ok := s.config.Providers.Get(providerID); ok {
			return true
		}
	}

	// Check known providers from catalog.
	for _, p := range s.knownProviders {
		if string(p.ID) == providerID {
			return true
		}
	}

	return false
}

// updatePreferredModelFields builds the fields map for persisting a preferred
// model change. Shared between UpdatePreferredModel and direct updateLocked
// callers (e.g. Load). Caller must hold writeMu.
func (s *ConfigStore) updatePreferredModelFields(c *Config, modelType SlotName, model SelectedModel) map[string]any {
	if c.Slots == nil {
		c.Slots = make(map[SlotName]SelectedModel)
	}
	c.Slots[modelType] = model
	s.pinPreferredModelLocked(modelType, model)

	return map[string]any{
		fmt.Sprintf("slots.%s", modelType): model,
	}
}

// SetCompactMode sets the compact mode setting and persists it.
func (s *ConfigStore) SetCompactMode(scope Scope, enabled bool) error {
	return s.update(scope, func(c *Config) map[string]any {
		c.ensureTUI().CompactMode = enabled
		return map[string]any{"options.tui.compact_mode": enabled}
	})
}

// SetProviderAPIKey sets the API key for a provider and persists it.
func (s *ConfigStore) SetProviderAPIKey(scope Scope, providerID string, apiKey any) error {
	var providerConfig ProviderConfig
	var exists bool
	var setKeyOrToken func()

	switch v := apiKey.(type) {
	case string:
		if err := s.SetConfigField(scope, fmt.Sprintf("providers.%s.api_key", providerID), v); err != nil {
			return fmt.Errorf("failed to save api key to config file: %w", err)
		}
		setKeyOrToken = func() { providerConfig.APIKey = v }
	case *oauth.Token:
		// Hold the refresh lock across the write so a peer's in-flight
		// token exchange cannot land on top of a credential the user just
		// obtained interactively — which would silently invalidate the
		// login they only just completed.
		if err := s.withRefreshLock(providerID, func() error {
			return s.SetConfigFields(scope, map[string]any{
				fmt.Sprintf("providers.%s.api_key", providerID): v.AccessToken,
				fmt.Sprintf("providers.%s.oauth", providerID):   v,
			})
		}); err != nil {
			return err
		}
		setKeyOrToken = func() {
			providerConfig.APIKey = v.AccessToken
			providerConfig.OAuthToken = v
			switch providerID {
			case string(catwalk.InferenceProviderCopilot):
				providerConfig.SetupGitHubCopilot()
			}
		}
	}

	cfg := s.Config()
	providerConfig, exists = cfg.Providers.Get(providerID)
	if exists {
		setKeyOrToken()
		cfg.Providers.Set(providerID, providerConfig)
	} else {
		var foundProvider *catwalk.Provider
		for _, p := range s.knownProviders {
			if string(p.ID) == providerID {
				foundProvider = &p
				break
			}
		}

		if foundProvider != nil {
			providerConfig = ProviderConfig{
				ID:           providerID,
				Name:         foundProvider.Name,
				BaseURL:      foundProvider.APIEndpoint,
				Type:         foundProvider.Type,
				Disable:      false,
				ExtraHeaders: make(map[string]string),
				ExtraParams:  make(map[string]string),
				Models:       wrapCatwalkModels(foundProvider.Models, nil),
			}
			setKeyOrToken()
		} else {
			return fmt.Errorf("provider with ID %s not found in known providers", providerID)
		}
		cfg.Providers.Set(providerID, providerConfig)
	}

	// Signal here rather than at a caller: this is where a credential
	// actually lands, and every path that stores one reaches it. A turn
	// parked in WaitForTokenChangeSince is waiting for exactly this.
	s.SignalAuthComplete(providerID)
	return nil
}

// ReloadFromDisk re-runs the config load/merge flow and updates the in-memory
// config atomically. It rebuilds the staleness snapshot after successful reload.
// On failure, the store state is rolled back to its previous state.
// Concurrent calls are serialised via writeMu.
func (s *ConfigStore) ReloadFromDisk(ctx context.Context) error {
	if s.workingDir == "" {
		return fmt.Errorf("cannot reload: working directory not set")
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.reloadFromDiskLocked(ctx)
}

// reloadFromDiskLocked performs the actual reload. Caller must hold writeMu.
func (s *ConfigStore) reloadFromDiskLocked(ctx context.Context) error {
	// Migrate deprecated disable_notifications before reloading config.
	migrateDisableNotifications()

	configPaths := lookupConfigs(s.workingDir)
	cfg, loadedPaths, layers, err := loadFromConfigPaths(ctx, configPaths)
	if err != nil {
		return fmt.Errorf("failed to reload config: %w", err)
	}

	// Apply defaults (using existing data directory if set)
	var dataDir string
	if cur := s.Config(); cur != nil && cur.Options != nil {
		dataDir = cur.Options.DataDirectory
	}
	cfg.setDefaults(s.workingDir, dataDir)

	// Captured before the recent-models sidecar takes over below, so a
	// pre-migration recent_models key still embedded in an old config
	// file seeds the sidecar the first time it is read.
	legacyRecentModels := cfg.RecentModels

	// Merge workspace config if present
	workspacePath := filepath.Join(cfg.Options.DataDirectory, fmt.Sprintf("%s.json", appName))
	if wsData, err := os.ReadFile(workspacePath); err == nil && len(wsData) > 0 {
		if !json.Valid(wsData) {
			return fmt.Errorf("invalid JSON in config file %s", workspacePath)
		}
		merged, mergeErr := loadFromBytes(append([][]byte{mustMarshalConfig(cfg)}, wsData))
		if mergeErr == nil {
			dataDir := cfg.Options.DataDirectory
			*cfg = *merged
			cfg.setDefaults(s.workingDir, dataDir)
			loadedPaths = append(loadedPaths, workspacePath)
			layers = append(layers, wsData)
		}
	}

	// Recent models live in a sidecar file next to each scope's config
	// file (see recent_models.go); load it last so it always wins over
	// any legacy recent_models key a config file still carries.
	cfg.RecentModels = loadRecentModels(s.globalDataPath, workspacePath, legacyRecentModels)

	// Validate hooks after all config merging is complete so matcher
	// regexes are recompiled on the reloaded config (mirrors Load).
	if err := cfg.ValidateHooks(); err != nil {
		return fmt.Errorf("invalid hook configuration on reload: %w", err)
	}
	if err := cfg.ValidateOptions(); err != nil {
		return fmt.Errorf("invalid options on reload: %w", err)
	}

	// Preserve runtime overrides
	overrides := s.overrides

	// Reapply model choices made in this instance. The global config file is
	// shared, so it may now name a model a sibling instance selected; a
	// reload triggered by an unrelated write must not swap the user's model
	// mid-session. An external edit to the config still takes effect for any
	// model type this instance never chose.
	maps.Copy(cfg.Slots, overrides.Slots)

	// Reconfigure providers
	env := env.New()
	resolver := NewShellVariableResolver(env)
	envOnlyResolver := NewEnvOnlyVariableResolver(env)
	dataTrust := computeDataFieldTrust(loadedPaths, layers, func(path string) bool {
		return trustedConfigLayer(path) || path == workspacePath
	})

	// Apply top-level env vars before configuring providers so variables
	// like AWS_PROFILE are visible to the AWS SDK credential chain.
	cfg.applyEnv(resolver)

	providers, err := Providers(cfg)
	if err != nil {
		if len(providers) == 0 {
			return fmt.Errorf("failed to load providers during reload: %w", err)
		}
		slog.Warn("Reload continuing with the previously known providers", "error", err)
	}

	if err := cfg.configureProviders(ctx, s, env, resolver, providers, withDataTrust(dataTrust)); err != nil {
		return fmt.Errorf("failed to configure providers during reload: %w", err)
	}

	// Finish cfg completely before publishing it. Everything below runs
	// against the local value, so a reader calling Config() concurrently
	// sees either the previous config or the fully built one, never a
	// version with models half-written or agents unresolved. A failure
	// here leaves the store untouched, which is why no rollback of
	// already-published state is needed.
	if !cfg.IsConfigured() {
		slog.Warn("No providers configured after reload")
	} else {
		resolved, resolveErr := resolveSelectedModels(cfg, providers)
		if resolveErr != nil {
			return fmt.Errorf("failed to configure selected models during reload: %w", resolveErr)
		}
		cfg.Slots[SlotMain] = resolved.Main
		cfg.Slots[SlotChore] = resolved.Chore
	}

	// Agent resolution reads only Options and AgentConfigs, so it runs
	// regardless of whether any provider is configured.
	prepareResolvedConfig(cfg)

	s.reapplyRuntimeMCP(cfg)
	s.setConfig(cfg)
	s.loadedPaths = loadedPaths
	s.resolver = resolver
	s.envOnlyResolver = envOnlyResolver
	s.dataTrust = dataTrust
	s.knownProviders = providers
	s.overrides = overrides
	s.workspacePath = workspacePath

	// Rebuild staleness tracking. Track every discovered config path, not
	// just the ones that loaded, so a config file created after this reload
	// is detected as a change on the next staleness check.
	s.captureStalenessSnapshot(append(slices.Clone(configPaths), loadedPaths...))

	return nil
}

// autoReload conditionally reloads config from disk after writes.
// It returns nil (no error) for expected skip cases: when auto-reload is
// disabled during load/reload flows, or when working directory is not set
// (e.g., during testing). Only actual reload failures return an error.
func (s *ConfigStore) autoReload(ctx context.Context) error {
	if s.workingDir == "" {
		return nil // Expected skip: working directory not set
	}
	// Skip if a reload is already in progress. This handles both
	// concurrent auto-reloads after parallel writes and re-entrant
	// calls from configureProviders during a reload.
	//
	// Note: if a write completes after the in-progress reload has
	// already read the config file, that write won't be reflected in
	// memory until the next reload. This is acceptable because writes
	// are rare and the next user action or file-watch tick will pick
	// up the change. Callers that need guaranteed fresh state after a
	// write should call ReloadFromDisk explicitly.
	if !s.writeMu.TryLock() {
		return nil
	}
	defer s.writeMu.Unlock()
	return s.reloadFromDiskLocked(ctx)
}
