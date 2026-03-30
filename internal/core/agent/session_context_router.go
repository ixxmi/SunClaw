package agent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/smallnest/goclaw/internal/core/namespaces"
	"github.com/smallnest/goclaw/internal/logger"
	"go.uber.org/zap"
)

// SessionContextRouter 管理“同一通道会话下的逻辑会话切换”状态。
// baseSessionKey 表示真实通道会话（channel:account:chat[:thread]），
// activeAlias 表示当前激活的逻辑别名；空字符串表示直接使用 baseSessionKey。
// aliases 记录 baseSessionKey 下所有已知逻辑会话别名及其对应的实际 session key。
type SessionContextRouter struct {
	mu          sync.RWMutex
	activeAlias map[string]string            // baseSessionKey -> alias
	aliases     map[string]map[string]string // baseSessionKey -> alias -> actual session key
	archived    map[string]map[string]bool   // baseSessionKey -> alias -> archived
	dataPath    string
}

type sessionContextRouterState struct {
	ActiveAlias map[string]string            `json:"active_alias"`
	Aliases     map[string]map[string]string `json:"aliases"`
	Archived    map[string]map[string]bool   `json:"archived,omitempty"`
}

func NewSessionContextRouter(dataDir string) *SessionContextRouter {
	r := &SessionContextRouter{
		activeAlias: make(map[string]string),
		aliases:     make(map[string]map[string]string),
		archived:    make(map[string]map[string]bool),
	}
	if dataDir != "" {
		r.dataPath = filepath.Join(dataDir, "session_context_routes.json")
		if err := r.loadFromDisk(); err != nil {
			logger.Warn("Failed to load session context routes from disk", zap.Error(err))
		}
	}
	return r
}

func (r *SessionContextRouter) Resolve(baseSessionKey string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	alias := strings.TrimSpace(r.activeAlias[baseSessionKey])
	if alias == "" {
		return baseSessionKey
	}
	if targets := r.aliases[baseSessionKey]; targets != nil {
		if actual := strings.TrimSpace(targets[alias]); actual != "" {
			return actual
		}
	}
	return baseSessionKey
}

func (r *SessionContextRouter) CurrentAlias(baseSessionKey string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return strings.TrimSpace(r.activeAlias[baseSessionKey])
}

func (r *SessionContextRouter) Switch(baseSessionKey, alias string) (string, error) {
	normalized := normalizeSessionAlias(alias)
	if normalized == "" {
		return baseSessionKey, nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.aliases[baseSessionKey] == nil {
		r.aliases[baseSessionKey] = make(map[string]string)
	}
	actual := strings.TrimSpace(r.aliases[baseSessionKey][normalized])
	if actual != "" {
		if r.isArchivedLocked(baseSessionKey, normalized) {
			return "", fmt.Errorf("alias %q is archived", normalized)
		}
		r.activeAlias[baseSessionKey] = normalized
		_ = r.saveToDisk()
		return actual, nil
	}

	actual = buildLogicalSessionKey(baseSessionKey, normalized)
	r.aliases[baseSessionKey][normalized] = actual
	r.activeAlias[baseSessionKey] = normalized
	_ = r.saveToDisk()
	return actual, nil
}

func (r *SessionContextRouter) Archive(baseSessionKey, alias string) (string, error) {
	normalized := normalizeSessionAlias(alias)
	if normalized == "" {
		return "", fmt.Errorf("invalid alias")
	}
	if normalized == "default" {
		return "", fmt.Errorf("default alias cannot be archived")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	aliases := r.aliases[baseSessionKey]
	if aliases == nil {
		return "", fmt.Errorf("alias %q not found", normalized)
	}
	actual := strings.TrimSpace(aliases[normalized])
	if actual == "" {
		return "", fmt.Errorf("alias %q not found", normalized)
	}
	if r.archived[baseSessionKey] == nil {
		r.archived[baseSessionKey] = make(map[string]bool)
	}
	r.archived[baseSessionKey][normalized] = true
	if strings.TrimSpace(r.activeAlias[baseSessionKey]) == normalized {
		delete(r.activeAlias, baseSessionKey)
	}
	if err := r.saveToDisk(); err != nil {
		return "", err
	}
	return actual, nil
}

func (r *SessionContextRouter) Unarchive(baseSessionKey, alias string) (string, error) {
	normalized := normalizeSessionAlias(alias)
	if normalized == "" {
		return "", fmt.Errorf("invalid alias")
	}
	if normalized == "default" {
		return "", fmt.Errorf("default alias cannot be unarchived")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	aliases := r.aliases[baseSessionKey]
	if aliases == nil {
		return "", fmt.Errorf("alias %q not found", normalized)
	}
	actual := strings.TrimSpace(aliases[normalized])
	if actual == "" {
		return "", fmt.Errorf("alias %q not found", normalized)
	}
	if archived := r.archived[baseSessionKey]; archived != nil {
		delete(archived, normalized)
		if len(archived) == 0 {
			delete(r.archived, baseSessionKey)
		}
	}
	if err := r.saveToDisk(); err != nil {
		return "", err
	}
	return actual, nil
}

func (r *SessionContextRouter) IsArchived(baseSessionKey, alias string) bool {
	normalized := normalizeSessionAlias(alias)
	if normalized == "" || normalized == "default" {
		return false
	}

	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.isArchivedLocked(baseSessionKey, normalized)
}

func (r *SessionContextRouter) isArchivedLocked(baseSessionKey, alias string) bool {
	return r.archived[baseSessionKey] != nil && r.archived[baseSessionKey][alias]
}

func (r *SessionContextRouter) Clear(baseSessionKey string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.activeAlias, baseSessionKey)
	_ = r.saveToDisk()
}

func (r *SessionContextRouter) Rename(baseSessionKey, oldAlias, newAlias string) (string, error) {
	oldNormalized := normalizeSessionAlias(oldAlias)
	newNormalized := normalizeSessionAlias(newAlias)
	if oldNormalized == "" || newNormalized == "" {
		return "", fmt.Errorf("invalid alias")
	}
	if oldNormalized == "default" || newNormalized == "default" {
		return "", fmt.Errorf("default alias cannot be renamed")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	aliases := r.aliases[baseSessionKey]
	if aliases == nil {
		return "", fmt.Errorf("alias %q not found", oldNormalized)
	}
	actual := strings.TrimSpace(aliases[oldNormalized])
	if actual == "" {
		return "", fmt.Errorf("alias %q not found", oldNormalized)
	}
	if strings.TrimSpace(aliases[newNormalized]) != "" {
		return "", fmt.Errorf("alias %q already exists", newNormalized)
	}

	delete(aliases, oldNormalized)
	aliases[newNormalized] = actual
	if strings.TrimSpace(r.activeAlias[baseSessionKey]) == oldNormalized {
		r.activeAlias[baseSessionKey] = newNormalized
	}
	if archived := r.archived[baseSessionKey]; archived != nil && archived[oldNormalized] {
		delete(archived, oldNormalized)
		archived[newNormalized] = true
	}
	if err := r.saveToDisk(); err != nil {
		return "", err
	}
	return actual, nil
}

func (r *SessionContextRouter) List(baseSessionKey string) []SessionContextEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()

	entries := []SessionContextEntry{{
		Alias:      "default",
		SessionKey: baseSessionKey,
		IsActive:   strings.TrimSpace(r.activeAlias[baseSessionKey]) == "",
		IsDefault:  true,
	}}

	active := strings.TrimSpace(r.activeAlias[baseSessionKey])
	if aliases := r.aliases[baseSessionKey]; aliases != nil {
		keys := make([]string, 0, len(aliases))
		for alias := range aliases {
			keys = append(keys, alias)
		}
		sort.Strings(keys)
		for _, alias := range keys {
			entries = append(entries, SessionContextEntry{
				Alias:      alias,
				SessionKey: aliases[alias],
				IsActive:   alias == active,
				IsArchived: r.isArchivedLocked(baseSessionKey, alias),
			})
		}
	}

	return entries
}

func (r *SessionContextRouter) loadFromDisk() error {
	if r.dataPath == "" {
		return nil
	}
	data, err := os.ReadFile(r.dataPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var state sessionContextRouterState
	if err := json.Unmarshal(data, &state); err != nil {
		return err
	}
	if state.ActiveAlias != nil {
		r.activeAlias = state.ActiveAlias
	}
	if state.Aliases != nil {
		r.aliases = state.Aliases
	}
	if state.Archived != nil {
		r.archived = state.Archived
	}
	if r.pruneLegacyRoutes() {
		_ = r.saveToDisk()
	}
	return nil
}

func (r *SessionContextRouter) saveToDisk() error {
	if r.dataPath == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(r.dataPath), 0o755); err != nil {
		return err
	}
	state := sessionContextRouterState{
		ActiveAlias: r.activeAlias,
		Aliases:     r.aliases,
		Archived:    r.archived,
	}
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(r.dataPath, data, 0o644)
}

type SessionContextEntry struct {
	Alias      string
	SessionKey string
	IsActive   bool
	IsDefault  bool
	IsArchived bool
}

func normalizeSessionAlias(alias string) string {
	cleaned := strings.Map(func(r rune) rune {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			return unicode.ToLower(r)
		case r == '-', r == '_':
			return r
		case unicode.IsSpace(r):
			return '-'
		default:
			return -1
		}
	}, strings.TrimSpace(alias))
	cleaned = strings.Trim(cleaned, "-_")
	for strings.Contains(cleaned, "--") {
		cleaned = strings.ReplaceAll(cleaned, "--", "-")
	}
	return cleaned
}

func buildLogicalSessionKey(baseSessionKey, alias string) string {
	return fmt.Sprintf("%s:session:%s", baseSessionKey, alias)
}

func (r *SessionContextRouter) pruneLegacyRoutes() bool {
	changed := false

	for baseSessionKey := range r.activeAlias {
		if isStructuredContextBaseKey(baseSessionKey) {
			continue
		}
		delete(r.activeAlias, baseSessionKey)
		changed = true
	}

	for baseSessionKey, aliases := range r.aliases {
		if !isStructuredContextBaseKey(baseSessionKey) {
			delete(r.aliases, baseSessionKey)
			delete(r.archived, baseSessionKey)
			changed = true
			continue
		}

		for alias, actual := range aliases {
			if isNamespacedContextSessionKey(actual) {
				continue
			}
			delete(aliases, alias)
			if archived := r.archived[baseSessionKey]; archived != nil {
				delete(archived, alias)
				if len(archived) == 0 {
					delete(r.archived, baseSessionKey)
				}
			}
			if strings.TrimSpace(r.activeAlias[baseSessionKey]) == alias {
				delete(r.activeAlias, baseSessionKey)
			}
			changed = true
		}

		if len(aliases) == 0 {
			delete(r.aliases, baseSessionKey)
			delete(r.archived, baseSessionKey)
			delete(r.activeAlias, baseSessionKey)
			changed = true
		}
	}

	for baseSessionKey := range r.archived {
		if !isStructuredContextBaseKey(baseSessionKey) {
			delete(r.archived, baseSessionKey)
			changed = true
		}
	}

	return changed
}

func isStructuredContextBaseKey(baseSessionKey string) bool {
	identity, ok := namespaces.FromSessionKey(strings.TrimSpace(baseSessionKey))
	return ok && strings.TrimSpace(identity.NamespaceKey()) != ""
}

func isNamespacedContextSessionKey(sessionKey string) bool {
	identity, ok := namespaces.FromSessionKey(strings.TrimSpace(sessionKey))
	return ok && strings.TrimSpace(identity.NamespaceKey()) != ""
}
