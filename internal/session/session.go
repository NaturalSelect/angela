package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/NaturalSelect/angela/internal/config"
	"github.com/NaturalSelect/angela/internal/db"
	"github.com/NaturalSelect/angela/internal/event"
	"github.com/NaturalSelect/angela/internal/pubsub"
	"github.com/google/uuid"
	"github.com/zeebo/xxh3"
)

// ErrSessionNotFound reports that a session the caller named does not
// exist. A write that matches no row is this, not a success, and a read
// that matches no row is this rather than a bare driver error — callers
// above this package answer 404 on it and must not have to know that
// the storage underneath is SQL.
var ErrSessionNotFound = errors.New("session not found")

type TodoStatus string

const (
	TodoStatusPending    TodoStatus = "pending"
	TodoStatusInProgress TodoStatus = "in_progress"
	TodoStatusCompleted  TodoStatus = "completed"
)

// HashID returns the XXH3 hash of a session ID (UUID) as a hex string.
func HashID(id string) string {
	h := xxh3.New()
	h.WriteString(id)
	return fmt.Sprintf("%x", h.Sum(nil))
}

type Todo struct {
	Content    string     `json:"content"`
	Status     TodoStatus `json:"status"`
	ActiveForm string     `json:"active_form"`
}

// HasIncompleteTodos returns true if there are any non-completed todos.
func HasIncompleteTodos(todos []Todo) bool {
	for _, todo := range todos {
		if todo.Status != TodoStatusCompleted {
			return true
		}
	}
	return false
}

type Session struct {
	ID              string
	ParentSessionID string
	Title           string

	// Agent names the agent the session runs. It is a projection of
	// ActiveAgent.Agent kept as its own column so callers that only
	// need the name do not have to decode the JSON; both are written
	// together by UpdateActiveAgent and cannot drift.
	Agent string

	// ActiveAgent is the session's own agent instance, reduced to the
	// part worth keeping: which agent, and which model it was pointed
	// at. The agent definition is deliberately absent — prompts, tools
	// and permissions are re-read from the config files on load.
	ActiveAgent      config.ActiveAgentState
	MessageCount     int64
	PromptTokens     int64
	CompletionTokens int64
	EstimatedUsage   bool
	SummaryMessageID string
	Cost             float64
	Todos            []Todo
	CreatedAt        int64
	UpdatedAt        int64
}

type Service interface {
	pubsub.Subscriber[Session]
	Create(ctx context.Context, title string) (Session, error)
	CreateTaskSession(ctx context.Context, toolCallID, parentSessionID, title string) (Session, error)
	Get(ctx context.Context, id string) (Session, error)
	GetLast(ctx context.Context) (Session, error)
	List(ctx context.Context) ([]Session, error)
	Save(ctx context.Context, session Session) (Session, error)
	UpdateTitleAndUsage(ctx context.Context, sessionID, title string, promptTokens, completionTokens int64, cost float64) error
	AddCost(ctx context.Context, sessionID string, delta float64) error
	UpdateActiveAgent(ctx context.Context, id string, state config.ActiveAgentState) error
	Rename(ctx context.Context, id string, title string) error
	Delete(ctx context.Context, id string) error

	// Agent tool session management
	CreateAgentToolSessionID(messageID, toolCallID string) string
	ParseAgentToolSessionID(sessionID string) (messageID string, toolCallID string, ok bool)
	IsAgentToolSession(sessionID string) bool
}

type service struct {
	*pubsub.Broker[Session]
	db *sql.DB
	q  *db.Queries

	// Estimated usage stays in memory so fetch-modify-save paths (e.g.,
	// updating todos or parent-session cost) do not rebuild a session from
	// SQLite and incorrectly clear the UI "~" marker.
	estimatedUsageMu sync.RWMutex
	estimatedUsage   map[string]bool
}

func (s *service) Create(ctx context.Context, title string) (Session, error) {
	dbSession, err := s.q.CreateSession(ctx, db.CreateSessionParams{
		ID:    uuid.New().String(),
		Title: title,
	})
	if err != nil {
		return Session{}, err
	}
	session, err := s.fromDBItem(dbSession)
	if err != nil {
		return Session{}, err
	}
	s.Publish(pubsub.CreatedEvent, session)
	event.SessionCreated()
	return session, nil
}

func (s *service) CreateTaskSession(ctx context.Context, toolCallID, parentSessionID, title string) (Session, error) {
	dbSession, err := s.q.CreateSession(ctx, db.CreateSessionParams{
		ID:              toolCallID,
		ParentSessionID: sql.NullString{String: parentSessionID, Valid: true},
		Title:           title,
	})
	if err != nil {
		return Session{}, err
	}
	session, err := s.fromDBItem(dbSession)
	if err != nil {
		return Session{}, err
	}
	s.Publish(pubsub.CreatedEvent, session)
	return session, nil
}

func (s *service) Delete(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("beginning transaction: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	qtx := s.q.WithTx(tx)

	dbSession, err := qtx.GetSessionByID(ctx, id)
	if err != nil {
		return err
	}
	if err = qtx.DeleteSessionMessages(ctx, dbSession.ID); err != nil {
		return fmt.Errorf("deleting session messages: %w", err)
	}
	if err = qtx.DeleteSessionFiles(ctx, dbSession.ID); err != nil {
		return fmt.Errorf("deleting session files: %w", err)
	}
	if err = qtx.DeleteSession(ctx, dbSession.ID); err != nil {
		return fmt.Errorf("deleting session: %w", err)
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("committing transaction: %w", err)
	}

	// The row is gone, so what it ran is moot: subscribers need the
	// identity of the session that went away, and refusing to announce
	// a deletion because the deleted row held a bad blob would leave
	// every listener holding it forever.
	session := s.sessionFromRow(dbSession)
	s.clearEstimatedUsageState(dbSession.ID)
	s.Publish(pubsub.DeletedEvent, session)
	event.SessionDeleted()
	return nil
}

func (s *service) Get(ctx context.Context, id string) (Session, error) {
	dbSession, err := s.q.GetSessionByID(ctx, id)
	if err != nil {
		return Session{}, notFound(err, id)
	}
	session, err := s.fromDBItem(dbSession)
	if err != nil {
		return Session{}, err
	}
	s.applyEstimatedUsageState(&session)
	return session, nil
}

func (s *service) GetLast(ctx context.Context) (Session, error) {
	dbSession, err := s.q.GetLastSession(ctx)
	if err != nil {
		return Session{}, notFound(err, "")
	}
	session, err := s.fromDBItem(dbSession)
	if err != nil {
		return Session{}, err
	}
	s.applyEstimatedUsageState(&session)
	return session, nil
}

// notFound restates "no such row" as this package's own error, so
// callers can tell a missing session from a database that is broken.
// Anything else is passed through untouched.
func notFound(err error, id string) error {
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if id == "" {
		return ErrSessionNotFound
	}
	return fmt.Errorf("%w: %s", ErrSessionNotFound, id)
}

func (s *service) Save(ctx context.Context, session Session) (Session, error) {
	todosJSON, err := marshalTodos(session.Todos)
	if err != nil {
		return Session{}, err
	}

	dbSession, err := s.q.UpdateSession(ctx, db.UpdateSessionParams{
		ID:               session.ID,
		Title:            session.Title,
		PromptTokens:     session.PromptTokens,
		CompletionTokens: session.CompletionTokens,
		SummaryMessageID: sql.NullString{
			String: session.SummaryMessageID,
			Valid:  session.SummaryMessageID != "",
		},
		Cost: session.Cost,
		Todos: sql.NullString{
			String: todosJSON,
			Valid:  todosJSON != "",
		},
	})
	if err != nil {
		return Session{}, err
	}
	estimatedUsage := session.EstimatedUsage
	s.setEstimatedUsageState(session.ID, estimatedUsage)
	session, err = s.fromDBItem(dbSession)
	if err != nil {
		return Session{}, err
	}
	session.EstimatedUsage = estimatedUsage
	s.Publish(pubsub.UpdatedEvent, session)
	return session, nil
}

// UpdateTitleAndUsage updates only the title and usage fields atomically.
// This is safer than fetching, modifying, and saving the entire session.
func (s *service) UpdateTitleAndUsage(ctx context.Context, sessionID, title string, promptTokens, completionTokens int64, cost float64) error {
	if err := s.q.UpdateSessionTitleAndUsage(ctx, db.UpdateSessionTitleAndUsageParams{
		ID:               sessionID,
		Title:            title,
		PromptTokens:     promptTokens,
		CompletionTokens: completionTokens,
		Cost:             cost,
	}); err != nil {
		return err
	}
	s.publishSessionUpdate(ctx, sessionID)
	return nil
}

// AddCost adds delta to a session's cost in a single statement.
//
// It is deliberately not expressed as Get, add, Save. Two roll-ups landing
// on the same ancestor would each read the same starting value and the
// later write would swallow the earlier increment; and because Save writes
// the whole row, it would also clobber a title, token count or todo list
// another writer changed in the meantime.
func (s *service) AddCost(ctx context.Context, sessionID string, delta float64) error {
	rows, err := s.q.AddSessionCost(ctx, db.AddSessionCostParams{
		ID:   sessionID,
		Cost: delta,
	})
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("%w: %s", ErrSessionNotFound, sessionID)
	}
	s.publishSessionUpdate(ctx, sessionID)
	return nil
}

// UpdateActiveAgent records the session's own agent instance. It is
// deliberately its own method: Save writes the caller's whole in-memory
// session back, so folding these columns into it would let any stale
// copy clobber them.
func (s *service) UpdateActiveAgent(ctx context.Context, id string, state config.ActiveAgentState) error {
	stateJSON, err := marshalActiveAgent(state)
	if err != nil {
		return err
	}
	rows, err := s.q.UpdateSessionActiveAgent(ctx, db.UpdateSessionActiveAgentParams{
		ID:          id,
		Agent:       sql.NullString{String: state.Agent, Valid: state.Agent != ""},
		ActiveAgent: sql.NullString{String: stateJSON, Valid: stateJSON != ""},
	})
	if err != nil {
		return err
	}
	if rows == 0 {
		return fmt.Errorf("%w: %s", ErrSessionNotFound, id)
	}
	s.publishSessionUpdate(ctx, id)
	return nil
}

// Rename updates only the title of a session without touching updated_at or
// usage fields.
func (s *service) Rename(ctx context.Context, id string, title string) error {
	if err := s.q.RenameSession(ctx, db.RenameSessionParams{
		ID:    id,
		Title: title,
	}); err != nil {
		return err
	}
	s.publishSessionUpdate(ctx, id)
	return nil
}

func (s *service) List(ctx context.Context) ([]Session, error) {
	dbSessions, err := s.q.ListSessions(ctx)
	if err != nil {
		return nil, err
	}
	sessions := make([]Session, len(dbSessions))
	for i, dbSession := range dbSessions {
		sessions[i], err = s.fromDBItem(dbSession)
		if err != nil {
			return nil, err
		}
		s.applyEstimatedUsageState(&sessions[i])
	}
	return sessions, nil
}

// publishSessionUpdate re-fetches a session and publishes an UpdatedEvent so
// that UI subscribers reflect title or usage changes.
func (s *service) publishSessionUpdate(ctx context.Context, sessionID string) {
	session, err := s.Get(ctx, sessionID)
	if err != nil {
		slog.Error("Failed to re-fetch session for event publish", "error", err, "sessionID", sessionID)
		return
	}
	s.Publish(pubsub.UpdatedEvent, session)
}

func (s *service) applyEstimatedUsageState(session *Session) {
	s.estimatedUsageMu.RLock()
	session.EstimatedUsage = s.estimatedUsage[session.ID]
	s.estimatedUsageMu.RUnlock()
}

func (s *service) setEstimatedUsageState(sessionID string, estimatedUsage bool) {
	s.estimatedUsageMu.Lock()
	defer s.estimatedUsageMu.Unlock()
	if estimatedUsage {
		s.estimatedUsage[sessionID] = true
		return
	}
	delete(s.estimatedUsage, sessionID)
}

func (s *service) clearEstimatedUsageState(sessionID string) {
	s.estimatedUsageMu.Lock()
	delete(s.estimatedUsage, sessionID)
	s.estimatedUsageMu.Unlock()
}

// fromDBItem rebuilds a session from its row. A stored active agent
// that will not decode is reported rather than dropped: the zero value
// reads downstream as "this session never picked anything", which would
// silently move a session onto a different agent, model and provider
// than the one it was running.
func (s *service) fromDBItem(item db.Session) (Session, error) {
	session := s.sessionFromRow(item)
	active, err := unmarshalActiveAgent(item.ActiveAgent.String)
	if err != nil {
		return Session{}, fmt.Errorf("decode the active agent of session %s: %w", item.ID, err)
	}
	session.ActiveAgent = active
	return session, nil
}

// sessionFromRow copies the columns whose loss cannot change what the
// session runs. Todos are logged rather than reported: an empty list is
// visible and recoverable, while refusing to load the session over it
// would strand the user from the conversation itself.
func (s *service) sessionFromRow(item db.Session) Session {
	todos, err := unmarshalTodos(item.Todos.String)
	if err != nil {
		slog.Error("Failed to unmarshal todos", "session_id", item.ID, "error", err)
	}
	return Session{
		ID:               item.ID,
		ParentSessionID:  item.ParentSessionID.String,
		Title:            item.Title,
		Agent:            item.Agent.String,
		MessageCount:     item.MessageCount,
		PromptTokens:     item.PromptTokens,
		CompletionTokens: item.CompletionTokens,
		SummaryMessageID: item.SummaryMessageID.String,
		Cost:             item.Cost,
		Todos:            todos,
		CreatedAt:        item.CreatedAt,
		UpdatedAt:        item.UpdatedAt,
	}
}

func marshalActiveAgent(state config.ActiveAgentState) (string, error) {
	if state.IsZero() {
		return "", nil
	}
	data, err := json.Marshal(state)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func unmarshalActiveAgent(data string) (config.ActiveAgentState, error) {
	if data == "" {
		return config.ActiveAgentState{}, nil
	}
	var state config.ActiveAgentState
	if err := json.Unmarshal([]byte(data), &state); err != nil {
		return config.ActiveAgentState{}, err
	}
	return state, nil
}

func marshalTodos(todos []Todo) (string, error) {
	if len(todos) == 0 {
		return "", nil
	}
	data, err := json.Marshal(todos)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func unmarshalTodos(data string) ([]Todo, error) {
	if data == "" {
		return []Todo{}, nil
	}
	var todos []Todo
	if err := json.Unmarshal([]byte(data), &todos); err != nil {
		return []Todo{}, err
	}
	return todos, nil
}

func NewService(q *db.Queries, conn *sql.DB) Service {
	broker := pubsub.NewBroker[Session]()
	return &service{
		Broker:         broker,
		db:             conn,
		q:              q,
		estimatedUsage: make(map[string]bool),
	}
}

// CreateAgentToolSessionID creates a session ID for agent tool sessions using the format "messageID$$toolCallID"
func (s *service) CreateAgentToolSessionID(messageID, toolCallID string) string {
	return fmt.Sprintf("%s$$%s", messageID, toolCallID)
}

// ParseAgentToolSessionID parses an agent tool session ID into its components
func (s *service) ParseAgentToolSessionID(sessionID string) (messageID string, toolCallID string, ok bool) {
	parts := strings.Split(sessionID, "$$")
	if len(parts) != 2 {
		return "", "", false
	}
	return parts[0], parts[1], true
}

// IsAgentToolSession checks if a session ID follows the agent tool session format
func (s *service) IsAgentToolSession(sessionID string) bool {
	_, _, ok := s.ParseAgentToolSessionID(sessionID)
	return ok
}
