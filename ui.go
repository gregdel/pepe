package main

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/dustin/go-humanize"
	polochon "github.com/odwrtw/polochon/lib"
	"github.com/odwrtw/polochon/lib/papi"
)

// Styles
var (
	primaryColor   = lipgloss.Color("99")
	secondaryColor = lipgloss.Color("39")
	mutedColor     = lipgloss.Color("240")
	terminalColor  = lipgloss.Color("212")

	selectedStyle = lipgloss.NewStyle().
			Background(secondaryColor).
			Bold(true).
			Foreground(lipgloss.Color("0"))

	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(primaryColor)

	terminalStyle = lipgloss.NewStyle().
			Foreground(terminalColor)

	pathHighlightStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(primaryColor)

	treeStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(mutedColor).
			Padding(1, 2)

	detailsStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(mutedColor).
			Padding(1, 2)

	helpStyle = lipgloss.NewStyle().
			Foreground(mutedColor).
			Italic(true)

	badgeStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("0")).
			Background(lipgloss.Color("240")).
			Padding(0, 1)

	sizeBadgeStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("0")).
			Background(lipgloss.Color("205")). // Pink
			Padding(0, 1)

	qualityBadgeStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("0")).
				Background(lipgloss.Color("99")). // Purple
				Padding(0, 1)

	codecBadgeStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("0")).
			Background(lipgloss.Color("214")). // Orange
			Padding(0, 1)
)

const detailSeparator = " · "

// formatQualityBadge formats video quality for display
func formatQualityBadge(q polochon.Quality) string {
	if q == "" {
		return ""
	}
	return string(q)
}

// formatContainer formats container for display
func formatContainer(c string) string {
	if c == "" {
		return ""
	}
	return strings.ToUpper(c)
}

// formatCodecs formats video and audio codecs with + separator
func formatCodecs(video, audio string) string {
	if video == "" && audio == "" {
		return ""
	}
	if video == "" {
		return audio
	}
	if audio == "" {
		return video
	}
	return fmt.Sprintf("%s + %s", video, audio)
}

// formatDateAdded formats date as YYYY-MM-DD
func formatDateAdded(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format("2006-01-02")
}

// formatGenres formats genre list
func formatGenres(genres []string) string {
	if len(genres) == 0 {
		return ""
	}
	return strings.Join(genres, ", ")
}

// Language display name map - only common languages
var languageNames = map[string]string{
	"en": "English",
	"fr": "French",
}

// formatLanguageName returns "English (en)" or "de_DE (de_DE)" for unknown
func formatLanguageName(lang polochon.Language) string {
	short := lang.ShortForm()
	if name, ok := languageNames[short]; ok {
		return fmt.Sprintf("%s (%s)", name, short)
	}
	// Fallback: show full language code
	return fmt.Sprintf("%s (%s)", string(lang), string(lang))
}

// formatSubtitles formats subtitle information for display
// Shows embedded subtitles first, then external files with sizes
func formatSubtitles(embeddedLangs []polochon.Language, externalSubs []*papi.Subtitle) string {
	hasEmbedded := len(embeddedLangs) > 0
	hasExternal := len(externalSubs) > 0

	if !hasEmbedded && !hasExternal {
		return "Subtitles: None"
	}

	var b strings.Builder
	b.WriteString("Subtitles:\n")

	// Embedded subtitles (in video container)
	for _, lang := range embeddedLangs {
		b.WriteString(fmt.Sprintf("  %s - embedded\n", formatLanguageName(lang)))
	}

	// External subtitle files
	for _, sub := range externalSubs {
		sizeStr := ""
		if sub.Size > 0 {
			sizeStr = fmt.Sprintf(", %s", humanize.Bytes(uint64(sub.Size)))
		}
		b.WriteString(fmt.Sprintf("  %s - external%s\n", formatLanguageName(sub.Lang), sizeStr))
	}

	return strings.TrimRight(b.String(), "\n")
}

// Model represents the application state (Elm architecture)
type Model struct {
	client *APIClient

	movieRadix   *RadixTree
	showRadix    *RadixTree
	titleToMedia map[string]*MediaItem
	movieCount   int
	showCount    int

	nav    *NavigationState
	width  int
	height int

	quitting bool

	refreshing bool
	spinner    spinner.Model

	playerRunning  bool
	playerCommand  string
	playerExecArgs []string
	playerStatus   PlayerStatus
	playerMode     PlayerMode
	playerError    string
}

type refreshLibraryMsg struct {
	snapshot LibrarySnapshot
	err      error
}

type PlayerStatus string

const (
	PlayerStatusIdle    PlayerStatus = "Idle"
	PlayerStatusRunning PlayerStatus = "Running"
	PlayerStatusError   PlayerStatus = "Error"
)

type PlayerMode string

const (
	PlayerModeGUI PlayerMode = "gui"
	PlayerModeKMS PlayerMode = "kms"
)

type playerFinishedMsg struct {
	err error
}

func initialModel(client *APIClient) Model {
	snapshot := client.getSnapshot()

	nav := newNavigationState()

	// Start at root - user will select Movies or Shows
	// Navigation state already initialized with empty path

	return Model{
		client:       client,
		movieRadix:   snapshot.movieRadix,
		showRadix:    snapshot.showRadix,
		titleToMedia: snapshot.titleToMedia,
		movieCount:   snapshot.movieCount,
		showCount:    snapshot.showCount,
		nav:          nav,
		width:        80,
		height:       24,
		spinner:      spinner.New(spinner.WithSpinner(spinner.Dot)),
		playerStatus: PlayerStatusIdle,
	}
}

func (m Model) Init() tea.Cmd {
	return nil
}

func refreshLibraryCmd(client *APIClient) tea.Cmd {
	return func() tea.Msg {
		err := client.fetchLibrary()
		if err != nil {
			return refreshLibraryMsg{err: err}
		}
		return refreshLibraryMsg{snapshot: client.getSnapshot()}
	}
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "ctrl+c", "q":
			m.quitting = true
			return m, tea.Quit

		case "r":
			m.refreshing = true
			return m, tea.Batch(refreshLibraryCmd(m.client), m.spinner.Tick)

		case "up", "k":
			return m.navigateUp(), nil

		case "down", "j":
			return m.navigateDown(), nil

		case "left", "h":
			return m.navigateLeft(), nil

		case "right", "l", "enter":
			return m.navigateRight()

		case "tab":
			return m.toggleSection(), nil
		}

	case refreshLibraryMsg:
		if msg.err != nil {
			m.refreshing = false
			return m, nil
		}
		m.movieRadix = msg.snapshot.movieRadix
		m.showRadix = msg.snapshot.showRadix
		m.titleToMedia = msg.snapshot.titleToMedia
		m.movieCount = msg.snapshot.movieCount
		m.showCount = msg.snapshot.showCount
		m.nav = newNavigationState()
		m.refreshing = false
		return m, nil

	case spinner.TickMsg:
		if m.refreshing {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			return m, cmd
		}
		return m, nil

	case playerFinishedMsg:
		m.playerRunning = false
		if msg.err != nil {
			m.playerStatus = PlayerStatusError
			m.playerError = msg.err.Error()
		} else {
			m.playerStatus = PlayerStatusIdle
			m.playerError = ""
		}
		return m, nil
	}

	return m, nil
}

func runPlayerCmd(args []string) tea.Cmd {
	if len(args) == 0 {
		return func() tea.Msg {
			return playerFinishedMsg{err: fmt.Errorf("empty player command")}
		}
	}
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return playerFinishedMsg{err: err}
	})
}

func quoteArg(arg string) string {
	if arg == "" {
		return "\"\""
	}
	if strings.ContainsAny(arg, " \t\n\"'\\") {
		escaped := strings.ReplaceAll(arg, "\"", "\\\"")
		return "\"" + escaped + "\""
	}
	return arg
}

func detectPlayerMode() PlayerMode {
	for _, name := range []string{"WAYLAND_DISPLAY", "DISPLAY"} {
		if os.Getenv(name) != "" {
			return PlayerModeGUI
		}
	}

	sessionType := strings.ToLower(strings.TrimSpace(os.Getenv("XDG_SESSION_TYPE")))
	if sessionType == "wayland" || sessionType == "x11" {
		return PlayerModeGUI
	}
	return PlayerModeKMS
}

func buildMPVArgs(title, videoURL string, subtitleURLs []string, mode PlayerMode) []string {
	args := []string{"mpv"}
	args = append(args, "--http-header-fields=X-Auth-Token: $POLOCHON_TOKEN")
	if mode == PlayerModeKMS {
		args = append(args,
			"--vo=gpu",
			"--gpu-context=drm",
			"--opengl-es=yes",
			"--fs",
			"--no-border",
			"--ontop",
		)
	}
	if title != "" {
		args = append(args, "--title="+title)
	}
	for _, sub := range subtitleURLs {
		if sub == "" {
			continue
		}
		args = append(args, "--sub-file="+sub)
	}
	args = append(args, videoURL)
	return args
}

func addQueryParam(rawURL, key, value string) string {
	if value == "" {
		return rawURL
	}
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	query := parsed.Query()
	query.Set(key, value)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func buildMPVCommand(args []string) string {

	var parts []string
	for _, arg := range args {
		parts = append(parts, quoteArg(arg))
	}
	return strings.Join(parts, " ")
}

func replaceMpvToken(args []string, token string) []string {
	if token == "" {
		return args
	}
	updated := make([]string, len(args))
	for i, arg := range args {
		updated[i] = strings.ReplaceAll(arg, "$POLOCHON_TOKEN", token)
	}
	return updated
}

func (m Model) navigateUp() Model {
	if m.nav.isAtRoot() {
		// At root level, toggle between Movies and Shows
		if m.nav.cursor > 0 {
			m.nav.cursor = 0
		}
	} else {
		m.nav.moveCursorUp()
	}
	return m
}

func (m Model) navigateDown() Model {
	if m.nav.isAtRoot() {
		// At root level, toggle between Movies and Shows
		if m.nav.cursor < 1 {
			m.nav.cursor = 1
		}
	} else {
		maxChildren := m.nav.getChildrenCount()
		m.nav.moveCursorDown(maxChildren)
	}
	return m
}

func (m Model) navigateLeft() Model {
	if m.nav.isAtSectionRoot() {
		// At section root, go back to root selection
		m.nav.path = []*RadixNode{}
		m.nav.cursor = int(m.nav.section)
	} else {
		// Go back one level in tree
		m.nav.goBack()
	}
	return m
}

func (m Model) navigateRight() (Model, tea.Cmd) {
	if m.nav.isAtRoot() {
		// At root level, enter selected section
		if m.nav.cursor == 0 {
			// Enter Movies
			m.nav.resetToSection(SectionMovies, m.movieRadix.Root())
		} else {
			// Enter Shows
			m.nav.resetToSection(SectionShows, m.showRadix.Root())
		}
		return m, nil
	}

	// Handle dynamic navigation (seasons/episodes)
	if m.nav.inDynamicNav {
		selectedChild := m.nav.getSelectedDynamicChild()
		if selectedChild == nil {
			return m, nil
		}

		if selectedChild.IsTerminalNode() {
			playerCommand, execArgs, mode, ok := m.buildEpisodeCommand(selectedChild)
			if !ok {
				m.playerStatus = PlayerStatusError
				m.playerCommand = ""
				m.playerExecArgs = nil
				m.playerMode = ""
				return m, nil
			}
			m.playerCommand = playerCommand
			m.playerExecArgs = execArgs
			m.playerRunning = true
			m.playerStatus = PlayerStatusRunning
			m.playerMode = mode
			m.playerError = ""
			return m, runPlayerCmd(execArgs)
		}

		// Enter season to see episodes
		m.nav.goForwardDynamic(selectedChild)
		return m, nil
	}

	// Get the selected child from radix tree
	selectedChild := m.nav.getSelectedChild()
	if selectedChild == nil {
		return m, nil
	}

	if selectedChild.IsTerminal {
		// This is a final media item
		media, ok := m.titleToMedia[selectedChild.FullString]
		if !ok {
			return m, nil
		}

		// If it's a show, enter season/episode navigation
		if media.Type == "show" {
			show, ok := m.client.getShow(media.ID)
			if !ok {
				return m, nil
			}

			// Create season nodes
			seasonNodes := createSeasonNodes(show)
			if len(seasonNodes) > 0 {
				m.nav.enterDynamicNav(seasonNodes, show.Title)
			}
			return m, nil
		}

		// It's a movie - start fake player
		playerCommand, execArgs, mode, ok := m.buildMovieCommand(media)
		if !ok {
			m.playerStatus = PlayerStatusError
			m.playerCommand = ""
			m.playerExecArgs = nil
			m.playerMode = ""
			return m, nil
		}
		m.playerCommand = playerCommand
		m.playerExecArgs = execArgs
		m.playerRunning = true
		m.playerStatus = PlayerStatusRunning
		m.playerMode = mode
		m.playerError = ""
		return m, runPlayerCmd(execArgs)
	}

	// Enter the selected node
	m.nav.goForward(selectedChild)
	return m, nil
}

func (m Model) toggleSection() Model {
	// Switch between Movies and Shows
	if m.nav.section == SectionMovies {
		m.nav.resetToSection(SectionShows, m.showRadix.Root())
	} else {
		m.nav.resetToSection(SectionMovies, m.movieRadix.Root())
	}
	return m
}

func (m Model) View() string {
	if m.quitting {
		return "Goodbye!\n"
	}

	leftWidth := m.width * 40 / 100
	rightWidth := m.width - leftWidth - 4
	contentHeight := m.height - 4

	leftPanel := m.renderLeftPanel(leftWidth, contentHeight)
	rightPanel := m.renderRightPanel(rightWidth, contentHeight)

	panels := lipgloss.JoinHorizontal(
		lipgloss.Top,
		leftPanel,
		rightPanel,
	)

	help := m.renderHelp()

	return lipgloss.JoinVertical(
		lipgloss.Left,
		panels,
		help,
	)
}

func (m Model) buildMovieCommand(media *MediaItem) (string, []string, PlayerMode, bool) {
	movie, ok := m.client.getMovie(media.ID)
	if !ok {
		return "", nil, "", false
	}
	videoURL, err := m.client.getDownloadURL(movie)
	if err != nil {
		return "", nil, "", false
	}
	videoURL = addQueryParam(videoURL, "title", media.Title)

	subs := make([]string, 0, len(movie.Subtitles))
	for _, sub := range movie.Subtitles {
		if sub == nil || sub.Embedded {
			continue
		}
		url, err := m.client.getDownloadURL(sub)
		if err != nil {
			continue
		}
		subs = append(subs, addQueryParam(url, "lang", string(sub.Lang)))
	}

	mode := detectPlayerMode()
	execArgs := buildMPVArgs(media.Title, videoURL, subs, mode)
	execArgs = replaceMpvToken(execArgs, m.client.getToken())
	cmdLine := buildMPVCommand(execArgs)
	return cmdLine, execArgs, mode, true
}

func (m Model) buildEpisodeCommand(node *DynamicNode) (string, []string, PlayerMode, bool) {
	if node == nil || node.Episode == nil {
		return "", nil, "", false
	}

	videoURL, err := m.client.getDownloadURL(node.Episode)
	if err != nil {
		return "", nil, "", false
	}
	videoURL = addQueryParam(videoURL, "title", node.Label)

	subs := make([]string, 0, len(node.Episode.Subtitles))
	for _, sub := range node.Episode.Subtitles {
		if sub == nil || sub.Embedded {
			continue
		}
		url, err := m.client.getDownloadURL(sub)
		if err != nil {
			continue
		}
		subs = append(subs, addQueryParam(url, "lang", string(sub.Lang)))
	}

	mode := detectPlayerMode()
	execArgs := buildMPVArgs(node.Label, videoURL, subs, mode)
	execArgs = replaceMpvToken(execArgs, m.client.getToken())
	cmdLine := buildMPVCommand(execArgs)
	return cmdLine, execArgs, mode, true
}

func (m Model) renderLeftPanel(width, height int) string {
	var b strings.Builder

	// Title
	title := "Polochon"
	if !m.nav.isAtRoot() {
		sectionName := "Movies"
		if m.nav.section == SectionShows {
			sectionName = "Shows"
		}
		title = "Polochon - " + sectionName
	}
	b.WriteString(titleStyle.Render(title))
	b.WriteString("\n\n")

	// Render tree
	if m.nav.isAtRoot() {
		// Root level - show sections
		b.WriteString(m.renderSectionItem("Movies", m.movieCount, m.nav.cursor == 0))
		b.WriteString("\n")
		b.WriteString(m.renderSectionItem("Shows", m.showCount, m.nav.cursor == 1))
	} else if m.nav.inDynamicNav {
		// Dynamic navigation (seasons/episodes)
		current := m.nav.getCurrentDynamicNode()
		if current != nil {
			for i, child := range current.Children {
				isSelected := (i == m.nav.cursor)
				b.WriteString(m.renderDynamicNodeItem(child, isSelected))
				b.WriteString("\n")
			}
		}
	} else {
		// Render current level's children in radix tree
		current := m.nav.getCurrentNode()
		if current != nil {
			for i, child := range current.Children {
				isSelected := (i == m.nav.cursor)
				b.WriteString(m.renderNodeItem(child, isSelected))
				b.WriteString("\n")
			}
		}
	}

	content := b.String()
	return treeStyle.
		Width(width - 4).
		Height(height).
		Render(content)
}

func (m Model) renderSectionItem(name string, count int, selected bool) string {
	prefix := "  "
	if selected {
		prefix = "> "
	}

	line := fmt.Sprintf("%s%s (%d)", prefix, name, count)

	if selected {
		return selectedStyle.Render(line)
	}
	return line
}

func (m Model) renderNodeItem(node *RadixNode, selected bool) string {
	prefix := "  "
	if selected {
		prefix = "> "
	}

	// Get current path for highlighting
	currentPath := m.nav.getCurrentPath()

	// Determine the display label
	var displayLabel string
	if node.IsTerminal && node.FullString != "" {
		// Terminal nodes: use full string
		displayLabel = node.FullString
	} else if currentPath != "" {
		// Non-terminal nodes: show accumulated path + label for better context
		displayLabel = currentPath + node.Label
	} else {
		// No current path (at root/section root): just show label
		displayLabel = node.Label
	}

	// Apply path highlighting to label
	styledLabel := displayLabel
	if currentPath != "" && strings.HasPrefix(displayLabel, currentPath) {
		// Split into highlighted prefix and normal remainder
		prefixPart := currentPath
		remainderPart := displayLabel[len(currentPath):]
		styledLabel = pathHighlightStyle.Render(prefixPart) + remainderPart
	}

	// Terminal nodes don't show count
	var line string
	if node.IsTerminal && node.SubtreeCount == 1 {
		line = fmt.Sprintf("%s%s", prefix, styledLabel)
	} else {
		line = fmt.Sprintf("%s%s (%d)", prefix, styledLabel, node.SubtreeCount)
	}

	// Different style for terminal nodes
	if selected {
		return selectedStyle.Render(line)
	} else if node.IsTerminal && node.SubtreeCount == 1 {
		return terminalStyle.Render(line)
	}
	return line
}

func (m Model) renderDynamicNodeItem(node *DynamicNode, selected bool) string {
	prefix := "  "
	if selected {
		prefix = "> "
	}

	label := node.Label
	if m.nav.dynamicTitle != "" {
		label = m.nav.dynamicTitle + detailSeparator + node.Label
	}

	// Episodes (terminal nodes) don't show count
	var line string
	if node.IsTerminalNode() {
		line = fmt.Sprintf("%s%s", prefix, label)
	} else {
		line = fmt.Sprintf("%s%s (%d)", prefix, label, node.SubtreeCount)
	}

	// Different style for episodes
	if selected {
		return selectedStyle.Render(line)
	} else if node.IsTerminalNode() {
		return terminalStyle.Render(line)
	}
	return line
}

func (m Model) renderRightPanel(width, height int) string {
	var b strings.Builder

	if m.nav.isAtRoot() {
		// Case 1: At root level, hovering Movies/Shows
		if m.nav.cursor == 0 {
			b.WriteString(m.renderSectionDetails("Movies", m.movieCount))
		} else {
			b.WriteString(m.renderSectionDetails("Shows", m.showCount))
		}
	} else if m.nav.inDynamicNav {
		// In dynamic navigation (seasons/episodes)
		selectedChild := m.nav.getSelectedDynamicChild()

		if selectedChild == nil {
			b.WriteString("No item selected")
		} else if selectedChild.Type == DynamicNodeSeason {
			// Season selected - show episode list
			b.WriteString(m.renderSeasonDetails(selectedChild))
		} else if selectedChild.Type == DynamicNodeEpisode {
			// Episode selected - show episode details
			b.WriteString(m.renderEpisodeDetails(selectedChild))
		}
	} else {
		// Get currently selected child
		selectedChild := m.nav.getSelectedChild()

		if selectedChild == nil {
			b.WriteString("No item selected")
		} else if selectedChild.IsTerminal {
			// Case 3: Terminal node - show media details
			media, ok := m.titleToMedia[selectedChild.FullString]
			if ok {
				b.WriteString(m.renderMediaDetails(media))
				b.WriteString("\n\n")
				b.WriteString(m.renderPlayerStatus())
			} else {
				b.WriteString("Media details not found")
			}
		} else {
			// Case 2: Non-terminal node - show children list
			b.WriteString(m.renderNodeChildren(selectedChild))
		}
	}

	content := b.String()
	return detailsStyle.
		Width(width - 4).
		Height(height).
		Render(content)
}

func (m Model) renderSectionDetails(sectionName string, count int) string {
	var b strings.Builder

	b.WriteString(titleStyle.Render(sectionName))
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("Total items: %d\n", count))
	b.WriteString("\n")
	b.WriteString("Press Enter or → to browse")

	return b.String()
}

func (m Model) renderPlayerStatus() string {
	var b strings.Builder
	status := m.playerStatus
	if status == "" {
		status = PlayerStatusIdle
	}

	b.WriteString(titleStyle.Render("Player"))
	b.WriteString("\n\n")
	b.WriteString(fmt.Sprintf("Status: %s\n", status))
	if m.playerMode != "" {
		b.WriteString(fmt.Sprintf("Mode: %s\n", m.playerMode))
	}
	if status == PlayerStatusError && m.playerError != "" {
		b.WriteString("\nError:\n")
		b.WriteString(m.playerError)
	}
	if status == PlayerStatusRunning && m.playerCommand != "" {
		b.WriteString("\nCommand:\n")
		b.WriteString(m.playerCommand)
	}

	return b.String()
}

func (m Model) renderNodeChildren(node *RadixNode) string {
	var b strings.Builder

	b.WriteString(titleStyle.Render("Children"))
	b.WriteString("\n\n")

	currentPath := m.nav.getCurrentPath()
	selectedPrefix := currentPath + node.Label

	if len(node.Children) == 0 {
		b.WriteString("No children")
	} else {
		b.WriteString(fmt.Sprintf("%d items in this group:\n\n", node.SubtreeCount))

		// Show first few children with their counts
		maxShow := 20
		for i, child := range node.Children {
			if i >= maxShow {
				remaining := len(node.Children) - maxShow
				b.WriteString(fmt.Sprintf("\n... and %d more", remaining))
				break
			}

			label := child.Label
			if child.IsTerminal && child.FullString != "" {
				label = child.FullString
			} else if selectedPrefix != "" {
				label = selectedPrefix + child.Label
			}

			styledLabel := label
			if selectedPrefix != "" && strings.HasPrefix(label, selectedPrefix) {
				remainder := label[len(selectedPrefix):]
				styledLabel = pathHighlightStyle.Render(selectedPrefix) + remainder
			}

			b.WriteString(fmt.Sprintf("  %s (%d)\n", styledLabel, child.SubtreeCount))
		}
	}

	return b.String()
}

func (m Model) renderMediaDetails(media *MediaItem) string {
	var b strings.Builder

	b.WriteString(titleStyle.Render(media.Title))
	b.WriteString("\n\n")

	// Get full movie details from cache
	movie, ok := m.client.getMovie(media.ID)
	if !ok {
		b.WriteString("Movie details not available")
		return b.String()
	}

	// Badge line - most important technical specs
	var badges []string
	if movie.Size > 0 {
		badges = append(badges, sizeBadgeStyle.Render(humanize.Bytes(uint64(movie.Size))))
	}
	if quality := formatQualityBadge(movie.Quality); quality != "" {
		badges = append(badges, qualityBadgeStyle.Render(quality))
	}
	if codecs := formatCodecs(movie.VideoCodec, movie.AudioCodec); codecs != "" {
		badges = append(badges, codecBadgeStyle.Render(codecs))
	}
	if container := formatContainer(movie.Container); container != "" {
		badges = append(badges, badgeStyle.Render(container))
	}

	if len(badges) > 0 {
		b.WriteString(strings.Join(badges, " "))
		b.WriteString("\n\n")
	}

	// Basic info line
	var infoItems []string
	if movie.Year > 0 {
		infoItems = append(infoItems, fmt.Sprintf("Year: %d", movie.Year))
	}
	if len(infoItems) > 0 {
		b.WriteString(strings.Join(infoItems, detailSeparator))
		b.WriteString("\n")
	}

	// Runtime and genres
	var detailItems []string
	if movie.Runtime > 0 {
		hours := movie.Runtime / 60
		mins := movie.Runtime % 60
		detailItems = append(detailItems, fmt.Sprintf("Runtime: %dh %dm", hours, mins))
	}
	if genres := formatGenres(movie.Genres); genres != "" {
		detailItems = append(detailItems, fmt.Sprintf("Genres: %s", genres))
	}
	if len(detailItems) > 0 {
		b.WriteString(strings.Join(detailItems, detailSeparator))
		b.WriteString("\n")
	}

	// Technical details
	if movie.ReleaseGroup != "" {
		b.WriteString("\n")
		b.WriteString(fmt.Sprintf("Release: %s", movie.ReleaseGroup))
	}
	if dateAdded := formatDateAdded(movie.DateAdded); dateAdded != "" {
		if movie.ReleaseGroup == "" {
			b.WriteString("\n")
		}
		b.WriteString("\n")
		b.WriteString(fmt.Sprintf("Added: %s", dateAdded))
	}

	// Subtitles
	subtitleInfo := formatSubtitles(movie.EmbeddedSubtitles, movie.Subtitles)
	b.WriteString("\n\n")
	b.WriteString(subtitleInfo)

	// Plot
	if movie.Plot != "" {
		b.WriteString("\n\n" + movie.Plot)
	}

	return b.String()
}

func (m Model) renderHelp() string {
	help := "hjkl/arrows: navigate · enter/l: select · h: back · tab: toggle section · r: refresh · q: quit"
	if m.playerRunning {
		help = help + detailSeparator + "player: running"
	}
	if m.refreshing {
		help = help + detailSeparator + "refreshing " + m.spinner.View()
	}
	return helpStyle.Render(help)
}

func (m Model) renderSeasonDetails(season *DynamicNode) string {
	var b strings.Builder

	b.WriteString(titleStyle.Render(season.Label))
	b.WriteString("\n\n")

	episodeCount := len(season.Children)
	b.WriteString(fmt.Sprintf("%d episodes\n\n", episodeCount))

	// Show episode list
	maxShow := 20
	for i, episode := range season.Children {
		if i >= maxShow {
			remaining := episodeCount - maxShow
			b.WriteString(fmt.Sprintf("\n... and %d more", remaining))
			break
		}
		b.WriteString(fmt.Sprintf("  %s\n", episode.Label))
	}

	return b.String()
}

func (m Model) renderEpisodeDetails(episodeNode *DynamicNode) string {
	var b strings.Builder

	episode := episodeNode.Episode
	if episode == nil {
		return "Episode details not available"
	}

	// Title: Show Title · Season X · Episode Y
	titleParts := []string{}
	showTitle := episode.ShowTitle
	if showTitle == "" {
		showTitle = m.nav.dynamicTitle
	}
	if showTitle != "" {
		titleParts = append(titleParts, showTitle)
	}
	if episode.Season > 0 {
		titleParts = append(titleParts, fmt.Sprintf("Season %d", episode.Season))
	}
	if episode.Episode > 0 {
		titleParts = append(titleParts, fmt.Sprintf("Episode %d", episode.Episode))
	}
	if len(titleParts) == 0 {
		titleParts = append(titleParts, episodeNode.Label)
	}
	b.WriteString(titleStyle.Render(strings.Join(titleParts, detailSeparator)))
	b.WriteString("\n\n")

	// Badge line - technical specs
	var badges []string
	if episode.Size > 0 {
		badges = append(badges, sizeBadgeStyle.Render(humanize.Bytes(uint64(episode.Size))))
	}
	if quality := formatQualityBadge(episode.Quality); quality != "" {
		badges = append(badges, qualityBadgeStyle.Render(quality))
	}
	if codecs := formatCodecs(episode.VideoCodec, episode.AudioCodec); codecs != "" {
		badges = append(badges, codecBadgeStyle.Render(codecs))
	}
	if container := formatContainer(episode.Container); container != "" {
		badges = append(badges, badgeStyle.Render(container))
	}

	if len(badges) > 0 {
		b.WriteString(strings.Join(badges, " "))
		b.WriteString("\n\n")
	}

	// Basic info line
	var infoItems []string
	if showTitle != "" {
		infoItems = append(infoItems, fmt.Sprintf("Show: %s", showTitle))
	}
	if episode.Aired != "" {
		infoItems = append(infoItems, fmt.Sprintf("Aired: %s", episode.Aired))
	}
	if episode.Runtime > 0 {
		infoItems = append(infoItems, fmt.Sprintf("Runtime: %d min", episode.Runtime))
	}
	if len(infoItems) > 0 {
		b.WriteString(strings.Join(infoItems, detailSeparator))
		b.WriteString("\n")
	}

	// Technical details
	if episode.ReleaseGroup != "" {
		b.WriteString("\n")
		b.WriteString(fmt.Sprintf("Release: %s", episode.ReleaseGroup))
	}
	if dateAdded := formatDateAdded(episode.DateAdded); dateAdded != "" {
		if episode.ReleaseGroup == "" {
			b.WriteString("\n")
		}
		b.WriteString("\n")
		b.WriteString(fmt.Sprintf("Added: %s", dateAdded))
	}

	// Subtitles
	subtitleInfo := formatSubtitles(episode.EmbeddedSubtitles, episode.Subtitles)
	b.WriteString("\n\n")
	b.WriteString(subtitleInfo)

	// Plot
	if episode.Plot != "" {
		b.WriteString("\n\n" + episode.Plot)
	}

	if m.playerCommand != "" {
		b.WriteString("\n\n")
		b.WriteString(m.renderPlayerStatus())
	}

	return b.String()
}
