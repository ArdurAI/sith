// SPDX-License-Identifier: Apache-2.0

// Package tui implements Sith's cache-first terminal fleet view.
package tui

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"

	"github.com/ArdurAI/sith/internal/connector"
	"github.com/ArdurAI/sith/internal/fleet"
	"github.com/ArdurAI/sith/internal/fleetcache"
	"github.com/ArdurAI/sith/internal/fleetrender"
	"github.com/ArdurAI/sith/internal/hydrate"
	"github.com/ArdurAI/sith/internal/localops"
)

type inputMode uint8

const (
	modeNormal inputMode = iota
	modeFilter
	modeCommand
	modeSearch
	modePortForward
)

// Syncer is the narrow background-I/O seam consumed by the TUI runtime.
type Syncer interface {
	Run(ctx context.Context) error
	SyncOnce(ctx context.Context) error
	SyncKinds(ctx context.Context, kinds ...string) error
}

// Model is a Bubble Tea model whose interaction path reads only fleetcache snapshots.
type Model struct {
	ctx       context.Context
	store     *fleetcache.Store
	syncer    Syncer
	lenses    []string
	lens      int
	scopes    []string
	input     string
	filter    string
	mode      inputMode
	inputAll  bool
	filterAll bool
	cursor    int
	width     int
	height    int
	coverage  bool
	version   uint64
	lastError string
	now       func() time.Time
	local     localops.Client
	panel     *localPanel
	pending   *localops.Target
	forwards  []*forwardEntry
}

type syncDoneMsg struct{ err error }
type cacheChangedMsg struct {
	version uint64
	err     error
}

type localPanel struct {
	title   string
	content string
	loading bool
	offset  int
	stream  io.ReadCloser
}

type forwardEntry struct {
	target  localops.Target
	ports   []localops.ForwardedPort
	session localops.ForwardSession
	err     error
	done    bool
}

type localTextMsg struct {
	title   string
	content string
	err     error
}

type localStreamMsg struct {
	stream io.ReadCloser
	err    error
}

type localChunkMsg struct {
	stream io.ReadCloser
	chunk  string
	err    error
}

type localExecDoneMsg struct{ err error }
type localEditDoneMsg struct{ err error }
type localForwardMsg struct {
	entry *forwardEntry
	err   error
}
type localForwardDoneMsg struct {
	entry *forwardEntry
	err   error
}

// NewModel validates and constructs the cold first-paint model.
func NewModel(ctx context.Context, store *fleetcache.Store, syncer Syncer) (*Model, error) {
	return newModel(ctx, store, syncer, nil)
}

// NewModelWithLocal adds explicit-context per-resource operations to the cache-first model.
func NewModelWithLocal(
	ctx context.Context,
	store *fleetcache.Store,
	syncer Syncer,
	local localops.Client,
) (*Model, error) {
	return newModel(ctx, store, syncer, local)
}

func newModel(ctx context.Context, store *fleetcache.Store, syncer Syncer, local localops.Client) (*Model, error) {
	if ctx == nil {
		return nil, fmt.Errorf("construct TUI model: context is nil")
	}
	if store == nil {
		return nil, fmt.Errorf("construct TUI model: store is nil")
	}
	if syncer == nil {
		return nil, fmt.Errorf("construct TUI model: syncer is nil")
	}
	return &Model{
		ctx:    ctx,
		store:  store,
		syncer: syncer,
		lenses: hydrate.TierOneKinds(),
		width:  120,
		height: 30,
		now:    time.Now,
		local:  local,
	}, nil
}

// Run starts the interactive alternate-screen fleet view.
func Run(ctx context.Context, store *fleetcache.Store, syncer Syncer, input io.Reader, output io.Writer) error {
	return RunWithLocal(ctx, store, syncer, nil, input, output)
}

// RunWithLocal starts the fleet view with local per-resource operations enabled.
func RunWithLocal(
	ctx context.Context,
	store *fleetcache.Store,
	syncer Syncer,
	local localops.Client,
	input io.Reader,
	output io.Writer,
) error {
	model, err := NewModelWithLocal(ctx, store, syncer, local)
	if err != nil {
		return err
	}
	program := tea.NewProgram(model, tea.WithContext(ctx), tea.WithInput(input), tea.WithOutput(output))
	if _, err := program.Run(); err != nil {
		return fmt.Errorf("run fleet TUI: %w", err)
	}
	return nil
}

// Init starts watch-backed hydration and store notifications independently.
func (model *Model) Init() tea.Cmd {
	return tea.Batch(model.watchCommand(), model.waitCommand(model.version))
}

// Update handles interaction entirely against cache state; only explicit sync commands call I/O.
func (model *Model) Update(message tea.Msg) (tea.Model, tea.Cmd) {
	switch typed := message.(type) {
	case tea.WindowSizeMsg:
		model.width = max(typed.Width, 40)
		model.height = max(typed.Height, 10)
	case cacheChangedMsg:
		if typed.err == nil {
			model.version = typed.version
			model.clampCursor()
			return model, model.waitCommand(model.version)
		}
	case syncDoneMsg:
		if typed.err != nil && !errors.Is(typed.err, hydrate.ErrPaused) && !errors.Is(typed.err, hydrate.ErrSyncInProgress) {
			model.lastError = typed.err.Error()
		} else if typed.err == nil {
			model.lastError = ""
		}
	case localTextMsg:
		if model.panel != nil {
			model.panel.loading = false
			model.panel.title = typed.title
			model.panel.content = typed.content
		}
		model.setLocalError(typed.err)
	case localStreamMsg:
		if typed.err != nil {
			model.setLocalError(typed.err)
			if model.panel != nil {
				model.panel.loading = false
			}
			break
		}
		if model.panel == nil {
			_ = typed.stream.Close()
			break
		}
		model.panel.loading, model.panel.stream = false, typed.stream
		return model, readLocalStream(typed.stream)
	case localChunkMsg:
		if model.panel == nil || model.panel.stream != typed.stream {
			_ = typed.stream.Close()
			break
		}
		model.appendPanel(typed.chunk)
		if typed.err != nil {
			_ = typed.stream.Close()
			model.panel.stream = nil
			if !errors.Is(typed.err, io.EOF) {
				model.setLocalError(typed.err)
			}
			break
		}
		return model, readLocalStream(typed.stream)
	case localExecDoneMsg:
		model.panel = &localPanel{title: "EXEC", content: "remote command ended; returned to the same fleet row\n"}
		model.setLocalError(typed.err)
	case localEditDoneMsg:
		model.panel = &localPanel{title: "YAML EDIT", content: "edit session ended; returned to the same fleet row\n"}
		model.setLocalError(typed.err)
	case localForwardMsg:
		if typed.err != nil {
			model.setLocalError(typed.err)
			if model.panel != nil {
				model.panel.loading = false
			}
			break
		}
		model.forwards = append(model.forwards, typed.entry)
		model.panel = &localPanel{title: "PORT-FORWARDS", content: model.forwardPanel()}
		return model, waitForward(typed.entry)
	case localForwardDoneMsg:
		typed.entry.err, typed.entry.done = typed.err, true
		if model.panel != nil && model.panel.title == "PORT-FORWARDS" {
			model.panel.content = model.forwardPanel()
		}
	case tea.KeyPressMsg:
		return model.handleKey(typed)
	}
	return model, nil
}

// View renders an alternate-screen frame from immutable cache snapshots only.
func (model *Model) View() tea.View {
	if model.panel != nil {
		return model.panelView()
	}
	snapshot := model.snapshot()
	allSnapshot := model.store.Query(fleet.LocalWorkspace, fleetcache.Query{Kind: model.currentLens(), MetadataOnly: true})
	allScopes := allSnapshot.Scopes
	renderLens := model.currentLens()
	if model.filterAll || (model.mode == modeSearch && model.inputAll) {
		renderLens = "Search"
	}
	table := fleetrender.Build(snapshot, fleetrender.Options{
		Lens: renderLens, MaxRows: model.maxRows(), Now: model.now().UTC(),
	})
	var content strings.Builder
	fmt.Fprintf(&content, "sith  ⎈ fleet: %s   scope: %s   lens: %s%s\n",
		fleetSummary(allScopes, allSnapshot.Coverage), model.scopeLabel(), model.currentLens(), syncGlyph(snapshot.Syncing))
	content.WriteString(contextStrip(allScopes))
	content.WriteString("\n")
	fmt.Fprintf(&content, "%s · %s · filter:%s\n", model.currentLens(), model.scopeLabel(), model.filterLabel())
	content.WriteString(model.renderRows(table))
	switch snapshot.State {
	case fleetcache.StateOffline:
		content.WriteString("\noffline — showing last-known fleet data\n")
	case fleetcache.StatePaused:
		content.WriteString("\nPAUSED — data frozen\n")
	case fleetcache.StateCold, fleetcache.StateWarming:
		content.WriteString("\nwarming contexts — ready cache rows render immediately\n")
	}
	if model.coverage {
		content.WriteString("\nCOVERAGE\n")
		for _, scope := range snapshot.Scopes {
			status := "unreachable"
			if scope.Reachable {
				status = "reachable"
			}
			fmt.Fprintf(&content, "  %-24s %s  last=%s\n", scope.Name, status, ageLabel(model.now, scope.ObservedAt))
		}
	}
	content.WriteString("\n")
	content.WriteString(fleetrender.CoverageLine(snapshot.Coverage))
	content.WriteString("   [c]overage [d]escribe [y]aml [l]ogs [s]hell [f]orward [e]dit [:]cmd [q]quit\n")
	if prompt := model.prompt(); prompt != "" {
		content.WriteString(prompt)
		content.WriteString("\n")
	}
	if model.lastError != "" {
		content.WriteString("warning: ")
		content.WriteString(model.lastError)
		content.WriteString("\n")
	} else if snapshot.LastError != "" {
		content.WriteString("warning: ")
		content.WriteString(snapshot.LastError)
		content.WriteString("\n")
	}
	view := tea.NewView(limitWidth(content.String(), model.width))
	view.AltScreen = true
	view.WindowTitle = "sith fleet"
	return view
}

func (model *Model) handleKey(message tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	key := message.String()
	if model.panel != nil {
		return model.handlePanelKey(key)
	}
	if model.mode != modeNormal {
		return model.handleInput(message)
	}
	switch key {
	case "q", "ctrl+c":
		model.closeLocalSessions()
		return model, tea.Quit
	case ":":
		model.mode, model.input = modeCommand, ""
	case "/":
		model.mode, model.input, model.inputAll = modeFilter, model.filter, false
	case "ctrl+k":
		model.mode, model.input, model.inputAll = modeSearch, model.filter, true
	case "c":
		model.coverage = !model.coverage
	case "ctrl+r":
		return model, model.syncCommand()
	case "d":
		return model.startLocalText("DESCRIBE", model.describeCommand)
	case "y":
		return model.startLocalText("YAML", model.yamlCommand)
	case "l":
		return model.startLogs()
	case "s":
		return model.startExec()
	case "e":
		return model.startEdit()
	case "f":
		return model.startForwardPrompt()
	case "up", "k":
		model.cursor = max(0, model.cursor-1)
	case "down", "j":
		model.cursor++
		model.clampCursor()
	case "0":
		model.scopes = nil
	case "esc":
		model.filter, model.filterAll = "", false
	default:
		if len(key) == 1 && key[0] >= '1' && key[0] <= '9' {
			model.selectScope(int(key[0] - '1'))
		}
	}
	return model, nil
}

func (model *Model) handleInput(message tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch message.String() {
	case "esc":
		model.mode, model.input, model.inputAll = modeNormal, "", false
		return model, nil
	case "enter":
		if model.mode == modeCommand {
			return model.applyCommand()
		}
		if model.mode == modePortForward {
			return model.startForward()
		}
		model.filter, model.filterAll = model.input, model.inputAll
		model.mode, model.input, model.inputAll = modeNormal, "", false
		return model, nil
	case "backspace":
		model.input = trimLastRune(model.input)
		model.cursor = 0
		return model, nil
	}
	if text := message.Key().Text; text != "" && message.Key().Mod == 0 {
		model.input += text
		model.cursor = 0
	}
	return model, nil
}

func (model *Model) applyCommand() (tea.Model, tea.Cmd) {
	command := strings.TrimSpace(model.input)
	model.mode, model.input, model.inputAll = modeNormal, "", false
	fields := strings.Fields(command)
	if len(fields) == 0 {
		return model, nil
	}
	switch strings.ToLower(fields[0]) {
	case "q", "quit":
		model.closeLocalSessions()
		return model, tea.Quit
	case "pause":
		if err := model.store.SetPaused(fleet.LocalWorkspace, true); err != nil {
			model.lastError = err.Error()
		}
	case "resume":
		if err := model.store.SetPaused(fleet.LocalWorkspace, false); err != nil {
			model.lastError = err.Error()
			return model, nil
		}
		return model, model.syncCommand()
	case "refresh":
		return model, model.syncCommand()
	case "ctx", "context":
		if len(fields) != 2 {
			model.lastError = "usage: :ctx <name>"
		} else {
			model.scopes = []string{fields[1]}
		}
	case "pf":
		model.panel = &localPanel{title: "PORT-FORWARDS", content: model.forwardPanel()}
	default:
		if len(fields) != 1 {
			model.lastError = "unknown command: " + fields[0]
			return model, nil
		}
		valid, added := model.setLens(fields[0])
		if !valid {
			model.lastError = "unknown command: " + fields[0]
		} else {
			model.lastError = ""
			if added {
				return model, model.syncLensCommand()
			}
		}
	}
	return model, nil
}

func (model *Model) snapshot() fleetcache.Snapshot {
	query := fleetcache.Query{Kind: model.currentLens(), Scopes: append([]string(nil), model.scopes...), MetadataOnly: true}
	expression := model.filter
	allKinds := model.filterAll
	if model.mode == modeFilter || model.mode == modeSearch {
		expression = model.input
		allKinds = model.inputAll
	}
	if allKinds {
		query.Kind = ""
	}
	if expression != "" {
		parsed, err := fleetcache.ParseSearch(expression)
		if err == nil {
			if !allKinds {
				parsed.Kind = query.Kind
			}
			parsed.Scopes = query.Scopes
			parsed.MetadataOnly = true
			query = parsed
		} else {
			query.Text = []string{strings.ToLower(expression)}
		}
	}
	return model.store.Query(fleet.LocalWorkspace, query)
}

func (model *Model) renderRows(table fleetrender.Table) string {
	var rendered bytes.Buffer
	tabular := tabwriter.NewWriter(&rendered, 0, 3, 2, ' ', 0)
	_, _ = fmt.Fprintln(tabular, "  \t"+strings.Join(table.Columns, "\t"))
	for index, row := range table.Rows {
		cursor := " "
		if index == model.cursor {
			cursor = ">"
		}
		_, _ = fmt.Fprintln(tabular, cursor+" \t"+strings.Join(row, "\t"))
	}
	if len(table.Rows) == 0 {
		_, _ = fmt.Fprintln(tabular, "  \t— no cached matches —")
	}
	_ = tabular.Flush()
	return rendered.String()
}

func (model *Model) syncCommand() tea.Cmd {
	return func() tea.Msg { return syncDoneMsg{err: model.syncer.SyncOnce(model.ctx)} }
}

func (model *Model) watchCommand() tea.Cmd {
	return func() tea.Msg { return syncDoneMsg{err: model.syncer.Run(model.ctx)} }
}

func (model *Model) syncLensCommand() tea.Cmd {
	kind := model.currentLens()
	return func() tea.Msg { return syncDoneMsg{err: model.syncer.SyncKinds(model.ctx, kind)} }
}

func (model *Model) waitCommand(after uint64) tea.Cmd {
	return func() tea.Msg {
		version, err := model.store.WaitForChange(model.ctx, fleet.LocalWorkspace, after)
		return cacheChangedMsg{version: version, err: err}
	}
}

func (model *Model) currentLens() string {
	return model.lenses[model.lens]
}

func (model *Model) setLens(value string) (bool, bool) {
	for index, lens := range model.lenses {
		if strings.HasPrefix(strings.ToLower(lens), strings.ToLower(value)) ||
			strings.HasPrefix(strings.ToLower(value), strings.ToLower(lens)) {
			model.lens = index
			model.cursor = 0
			model.filterAll = false
			return true, false
		}
	}
	if !validResourceToken(value) {
		return false, false
	}
	canonical := strings.ToUpper(value[:1]) + strings.ToLower(value[1:])
	model.lenses = append(model.lenses, canonical)
	model.lens = len(model.lenses) - 1
	model.cursor = 0
	model.filterAll = false
	return true, true
}

func validResourceToken(value string) bool {
	if value == "" {
		return false
	}
	for index := range len(value) {
		character := value[index]
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || character == '.' || character == '-' || character == '_' {
			continue
		}
		return false
	}
	return value[0] != '.' && value[0] != '-' && value[0] != '_'
}

func (model *Model) clampCursor() {
	records := model.snapshot().Records
	if len(records) == 0 {
		model.cursor = 0
	} else if model.cursor >= len(records) {
		model.cursor = len(records) - 1
	}
}

func (model *Model) selectScope(index int) {
	scopes := model.store.Query(fleet.LocalWorkspace, fleetcache.Query{MetadataOnly: true}).Scopes
	if index >= 0 && index < len(scopes) {
		model.scopes = []string{scopes[index].Name}
		model.cursor = 0
	}
}

func (model *Model) scopeLabel() string {
	if len(model.scopes) == 0 {
		return "all-clusters"
	}
	return strings.Join(model.scopes, ",")
}

func (model *Model) maxRows() int {
	rows := model.height - 9
	if model.coverage {
		rows -= 5
	}
	return max(rows, 1)
}

func (model *Model) prompt() string {
	switch model.mode {
	case modeFilter:
		return "/" + model.input
	case modeCommand:
		return ":" + model.input
	case modeSearch:
		return "search> " + model.input
	case modePortForward:
		return "port-forward> " + model.input
	default:
		return ""
	}
}

func fleetSummary(scopes []connector.Scope, coverage fleet.Coverage) string {
	stale := len(coverage.Stale)
	unreachable := len(coverage.Unreachable)
	fresh := max(coverage.Reachable-stale, 0)
	return fmt.Sprintf("%d ctx (%d✓ %d~ %d✗)", len(scopes), fresh, stale, unreachable)
}

func contextStrip(scopes []connector.Scope) string {
	parts := []string{"contexts: [0] all"}
	for index, scope := range scopes {
		if index == 9 {
			parts = append(parts, "…")
			break
		}
		parts = append(parts, fmt.Sprintf("[%d] %s", index+1, scope.Name))
	}
	return strings.Join(parts, "  ")
}

func syncGlyph(syncing bool) string {
	if syncing {
		return "  ⟳"
	}
	return ""
}

func (model *Model) filterLabel() string {
	value := model.filter
	if model.mode == modeFilter || model.mode == modeSearch {
		value = model.input
	}
	if value == "" {
		return "(none)"
	}
	return value
}

func ageLabel(now func() time.Time, observed time.Time) string {
	if observed.IsZero() {
		return "never"
	}
	age := now().Sub(observed)
	if age < time.Minute {
		return fmt.Sprintf("%ds", int(age.Seconds()))
	}
	return fmt.Sprintf("%dm", int(age.Minutes()))
}

func trimLastRune(value string) string {
	if value == "" {
		return ""
	}
	_, size := utf8.DecodeLastRuneInString(value)
	return value[:len(value)-size]
}

func limitWidth(value string, width int) string {
	lines := strings.Split(value, "\n")
	for index, line := range lines {
		runes := []rune(line)
		if len(runes) > width {
			lines[index] = string(runes[:width])
		}
	}
	return strings.Join(lines, "\n")
}
