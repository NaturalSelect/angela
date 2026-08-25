package styles

import (
	"image/color"

	"charm.land/bubbles/v2/filepicker"
	"charm.land/bubbles/v2/help"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/glamour/v2/ansi"
	"charm.land/lipgloss/v2"
	"github.com/NaturalSelect/angela/internal/ui/diffview"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/exp/charmtone"
)

// quickStyleOpts is the palette of colors used by quickStyle to simplify the
// process of building a theme.
type quickStyleOpts struct {
	// Brand.
	primary   color.Color
	secondary color.Color
	accent    color.Color
	keyword   color.Color

	// Default foreground and background colors.
	fgBase color.Color
	bgBase color.Color

	// Low-contrast dividers, separators, and rule lines.
	separator color.Color

	fgSubtle     color.Color
	fgMoreSubtle color.Color
	fgMostSubtle color.Color

	// Contrast pairings: foregrounds designed to sit on top of a
	// matching background role.
	onPrimary color.Color // foreground on primary backgrounds.

	bgMostVisible  color.Color
	bgLessVisible  color.Color
	bgLeastVisible color.Color

	// Surface behind a selected row. It reads as a raised row rather than
	// as a brand mark, so selection never becomes the loudest thing on
	// screen.
	bgSelected color.Color

	// Statuses.
	destructive       color.Color
	error             color.Color
	warning           color.Color
	warningSubtle     color.Color
	attention         color.Color
	busy              color.Color
	info              color.Color
	infoMoreSubtle    color.Color
	infoMostSubtle    color.Color
	success           color.Color
	successMoreSubtle color.Color
	successMostSubtle color.Color

	// ANSI 16-color palette. These remap the basic terminal colors that
	// programs emit (e.g. bang-mode shell output) onto legible, on-brand
	// colors instead of leaving them to the user's terminal defaults.
	// Normal intensity.
	ansiBlack   color.Color
	ansiRed     color.Color
	ansiGreen   color.Color
	ansiYellow  color.Color
	ansiBlue    color.Color
	ansiMagenta color.Color
	ansiCyan    color.Color
	ansiWhite   color.Color
	// Bright intensity.
	ansiBrightBlack   color.Color
	ansiBrightRed     color.Color
	ansiBrightGreen   color.Color
	ansiBrightYellow  color.Color
	ansiBrightBlue    color.Color
	ansiBrightMagenta color.Color
	ansiBrightCyan    color.Color
	ansiBrightWhite   color.Color
}

// quickStyle builds the default Styles (that is, the default theme, Charmtone
// Pantera) from a palette of semi-semanticly-named colors.
//
// The idea here is that you can do most of the work on a theme with quickStyle,
// then add overrides as needed.
func quickStyle(o quickStyleOpts) Styles {
	var (
		base   = lipgloss.NewStyle().Foreground(o.fgBase)
		muted  = lipgloss.NewStyle().Foreground(o.fgMoreSubtle)
		subtle = lipgloss.NewStyle().Foreground(o.fgMostSubtle)
		s      Styles
	)

	s.Background = o.bgBase

	// Working animations travel a brightness ramp rather than a hue ramp, so
	// a long-running turn shimmers instead of cycling colors.
	s.WorkingGradFromColor = o.fgMostSubtle
	s.WorkingGradToColor = o.fgBase
	s.WorkingLabelColor = o.fgMostSubtle
	s.WorkingTimerColor = o.fgMostSubtle

	s.TextInput = textinput.Styles{
		Focused: textinput.StyleState{
			Text:        base,
			Placeholder: base.Foreground(o.fgMostSubtle),
			Prompt:      base.Foreground(o.fgSubtle),
			Suggestion:  base.Foreground(o.fgMostSubtle),
		},
		Blurred: textinput.StyleState{
			Text:        base.Foreground(o.fgMoreSubtle),
			Placeholder: base.Foreground(o.fgMostSubtle),
			Prompt:      base.Foreground(o.fgMoreSubtle),
			Suggestion:  base.Foreground(o.fgMostSubtle),
		},
		Cursor: textinput.CursorStyle{
			Color: o.fgBase,
			Shape: tea.CursorBlock,
			Blink: true,
		},
	}

	s.Editor.Textarea = textarea.Styles{
		Focused: textarea.StyleState{
			Base:             base,
			Text:             base,
			LineNumber:       base.Foreground(o.fgMostSubtle),
			CursorLine:       base,
			CursorLineNumber: base.Foreground(o.fgMostSubtle),
			Placeholder:      base.Foreground(o.fgMostSubtle),
			Prompt:           base.Foreground(o.fgSubtle),
		},
		Blurred: textarea.StyleState{
			Base:             base,
			Text:             base.Foreground(o.fgMoreSubtle),
			LineNumber:       base.Foreground(o.fgMoreSubtle),
			CursorLine:       base,
			CursorLineNumber: base.Foreground(o.fgMoreSubtle),
			Placeholder:      base.Foreground(o.fgMostSubtle),
			Prompt:           base.Foreground(o.fgMoreSubtle),
		},
		Cursor: textarea.CursorStyle{
			Color: o.fgBase,
			Shape: tea.CursorBlock,
			Blink: true,
		},
	}

	s.Markdown = ansi.StyleConfig{
		Document: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				// BlockPrefix: "\n",
				// BlockSuffix: "\n",
				Color: hex(o.fgSubtle),
			},
			// Margin: new(uint(defaultMargin)),
		},
		BlockQuote: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{},
			Indent:         new(uint(1)),
			IndentToken:    new("│ "),
		},
		List: ansi.StyleList{
			LevelIndent: defaultListIndent,
		},
		Heading: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				BlockSuffix: "\n",
				Color:       hex(o.fgBase),
				Bold:        new(true),
			},
		},
		// Headings are told apart by color and weight, not by literal hash
		// marks — a rendered document should not still show its markup.
		H1: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Color: hex(o.fgBase),
				Bold:  new(true),
			},
		},
		H2: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Color: hex(o.fgBase),
				Bold:  new(true),
			},
		},
		H3: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Color: hex(o.fgSubtle),
				Bold:  new(true),
			},
		},
		H4: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Color: hex(o.fgBase),
				Bold:  new(true),
			},
		},
		H5: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Color: hex(o.fgSubtle),
				Bold:  new(true),
			},
		},
		H6: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Color: hex(o.fgMoreSubtle),
				Bold:  new(false),
			},
		},
		Strikethrough: ansi.StylePrimitive{
			CrossedOut: new(true),
		},
		Emph: ansi.StylePrimitive{
			Italic: new(true),
		},
		Strong: ansi.StylePrimitive{
			Bold: new(true),
		},
		HorizontalRule: ansi.StylePrimitive{
			Color:  hex(o.separator),
			Format: "\n───\n",
		},
		Item: ansi.StylePrimitive{
			BlockPrefix: "• ",
		},
		Enumeration: ansi.StylePrimitive{
			BlockPrefix: ". ",
		},
		Task: ansi.StyleTask{
			StylePrimitive: ansi.StylePrimitive{},
			Ticked:         "[✓] ",
			Unticked:       "[ ] ",
		},
		Link: ansi.StylePrimitive{
			Color:     hex(o.fgSubtle),
			Underline: new(true),
		},
		LinkText: ansi.StylePrimitive{
			Color: hex(o.fgSubtle),
			Bold:  new(true),
		},
		Image: ansi.StylePrimitive{
			Color:     hex(o.fgMoreSubtle),
			Underline: new(true),
		},
		ImageText: ansi.StylePrimitive{
			Color:  hex(o.fgMoreSubtle),
			Format: "Image: {{.text}} →",
		},
		Code: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Prefix:          " ",
				Suffix:          " ",
				Color:           hex(o.fgBase),
				BackgroundColor: hex(o.bgLessVisible),
			},
		},
		CodeBlock: ansi.StyleCodeBlock{
			StyleBlock: ansi.StyleBlock{
				StylePrimitive: ansi.StylePrimitive{
					Color: hex(o.bgLessVisible),
				},
				Margin: new(uint(defaultMargin)),
			},
			Chroma: &ansi.Chroma{
				Text: ansi.StylePrimitive{
					Color: hex(o.fgSubtle),
				},
				Error: ansi.StylePrimitive{
					Color:           hex(o.onPrimary),
					BackgroundColor: hex(o.error),
				},
				Comment: ansi.StylePrimitive{
					Color: hex(o.fgMostSubtle),
				},
				CommentPreproc: ansi.StylePrimitive{
					Color: hex(charmtone.Bengal),
				},
				Keyword: ansi.StylePrimitive{
					Color: hex(o.info),
				},
				KeywordReserved: ansi.StylePrimitive{
					Color: hex(charmtone.Pony),
				},
				KeywordNamespace: ansi.StylePrimitive{
					Color: hex(charmtone.Pony),
				},
				KeywordType: ansi.StylePrimitive{
					Color: hex(charmtone.Guppy),
				},
				Operator: ansi.StylePrimitive{
					Color: hex(charmtone.Salmon),
				},
				Punctuation: ansi.StylePrimitive{
					Color: hex(o.warningSubtle),
				},
				Name: ansi.StylePrimitive{
					Color: hex(o.fgSubtle),
				},
				NameBuiltin: ansi.StylePrimitive{
					Color: hex(charmtone.Cheeky),
				},
				NameTag: ansi.StylePrimitive{
					Color: hex(charmtone.Mauve),
				},
				NameAttribute: ansi.StylePrimitive{
					Color: hex(charmtone.Hazy),
				},
				NameClass: ansi.StylePrimitive{
					Color:     hex(charmtone.Salt),
					Underline: new(true),
					Bold:      new(true),
				},
				NameDecorator: ansi.StylePrimitive{
					Color: hex(charmtone.Citron),
				},
				NameFunction: ansi.StylePrimitive{
					Color: hex(o.successMostSubtle),
				},
				LiteralNumber: ansi.StylePrimitive{
					Color: hex(o.success),
				},
				LiteralString: ansi.StylePrimitive{
					Color: hex(charmtone.Cumin),
				},
				LiteralStringEscape: ansi.StylePrimitive{
					Color: hex(o.successMoreSubtle),
				},
				GenericDeleted: ansi.StylePrimitive{
					Color: hex(o.destructive),
				},
				GenericEmph: ansi.StylePrimitive{
					Italic: new(true),
				},
				GenericInserted: ansi.StylePrimitive{
					Color: hex(o.successMostSubtle),
				},
				GenericStrong: ansi.StylePrimitive{
					Bold: new(true),
				},
				GenericSubheading: ansi.StylePrimitive{
					Color: hex(o.fgMoreSubtle),
				},
				Background: ansi.StylePrimitive{
					BackgroundColor: hex(o.bgLessVisible),
				},
			},
		},
		Table: ansi.StyleTable{
			StyleBlock: ansi.StyleBlock{
				StylePrimitive: ansi.StylePrimitive{},
			},
		},
		DefinitionDescription: ansi.StylePrimitive{
			BlockPrefix: "\n ",
		},
	}

	// QuietMarkdown style - muted colors on subtle background for thinking content.
	plainBg := hex(o.bgLeastVisible)
	plainFg := hex(o.fgMoreSubtle)
	s.QuietMarkdown = ansi.StyleConfig{
		Document: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Color:           plainFg,
				BackgroundColor: plainBg,
			},
		},
		BlockQuote: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Color:           plainFg,
				BackgroundColor: plainBg,
			},
			Indent:      new(uint(1)),
			IndentToken: new("│ "),
		},
		List: ansi.StyleList{
			LevelIndent: defaultListIndent,
		},
		Heading: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				BlockSuffix:     "\n",
				Bold:            new(true),
				Color:           plainFg,
				BackgroundColor: plainBg,
			},
		},
		H1: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Prefix:          " ",
				Suffix:          " ",
				Bold:            new(true),
				Color:           plainFg,
				BackgroundColor: plainBg,
			},
		},
		H2: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Prefix:          "## ",
				Color:           plainFg,
				BackgroundColor: plainBg,
			},
		},
		H3: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Prefix:          "### ",
				Color:           plainFg,
				BackgroundColor: plainBg,
			},
		},
		H4: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Prefix:          "#### ",
				Color:           plainFg,
				BackgroundColor: plainBg,
			},
		},
		H5: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Prefix:          "##### ",
				Color:           plainFg,
				BackgroundColor: plainBg,
			},
		},
		H6: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Prefix:          "###### ",
				Color:           plainFg,
				BackgroundColor: plainBg,
			},
		},
		Strikethrough: ansi.StylePrimitive{
			CrossedOut:      new(true),
			Color:           plainFg,
			BackgroundColor: plainBg,
		},
		Emph: ansi.StylePrimitive{
			Italic:          new(true),
			Color:           plainFg,
			BackgroundColor: plainBg,
		},
		Strong: ansi.StylePrimitive{
			Bold:            new(true),
			Color:           plainFg,
			BackgroundColor: plainBg,
		},
		HorizontalRule: ansi.StylePrimitive{
			Format:          "\n--------\n",
			Color:           plainFg,
			BackgroundColor: plainBg,
		},
		Item: ansi.StylePrimitive{
			BlockPrefix:     "• ",
			Color:           plainFg,
			BackgroundColor: plainBg,
		},
		Enumeration: ansi.StylePrimitive{
			BlockPrefix:     ". ",
			Color:           plainFg,
			BackgroundColor: plainBg,
		},
		Task: ansi.StyleTask{
			StylePrimitive: ansi.StylePrimitive{
				Color:           plainFg,
				BackgroundColor: plainBg,
			},
			Ticked:   "[✓] ",
			Unticked: "[ ] ",
		},
		Link: ansi.StylePrimitive{
			Underline:       new(true),
			Color:           plainFg,
			BackgroundColor: plainBg,
		},
		LinkText: ansi.StylePrimitive{
			Bold:            new(true),
			Color:           plainFg,
			BackgroundColor: plainBg,
		},
		Image: ansi.StylePrimitive{
			Underline:       new(true),
			Color:           plainFg,
			BackgroundColor: plainBg,
		},
		ImageText: ansi.StylePrimitive{
			Format:          "Image: {{.text}} →",
			Color:           plainFg,
			BackgroundColor: plainBg,
		},
		Code: ansi.StyleBlock{
			StylePrimitive: ansi.StylePrimitive{
				Prefix:          " ",
				Suffix:          " ",
				Color:           plainFg,
				BackgroundColor: plainBg,
			},
		},
		CodeBlock: ansi.StyleCodeBlock{
			StyleBlock: ansi.StyleBlock{
				StylePrimitive: ansi.StylePrimitive{
					Color:           plainFg,
					BackgroundColor: plainBg,
				},
				Margin: new(uint(defaultMargin)),
			},
		},
		Table: ansi.StyleTable{
			StyleBlock: ansi.StyleBlock{
				StylePrimitive: ansi.StylePrimitive{
					Color:           plainFg,
					BackgroundColor: plainBg,
				},
			},
		},
		DefinitionDescription: ansi.StylePrimitive{
			BlockPrefix:     "\n ",
			Color:           plainFg,
			BackgroundColor: plainBg,
		},
	}

	s.Help = help.Styles{
		ShortKey:       base.Foreground(o.fgSubtle).Bold(true),
		ShortDesc:      base.Foreground(o.fgMostSubtle),
		ShortSeparator: base.Foreground(o.separator),
		Ellipsis:       base.Foreground(o.separator),
		FullKey:        base.Foreground(o.fgSubtle).Bold(true),
		FullDesc:       base.Foreground(o.fgMostSubtle),
		FullSeparator:  base.Foreground(o.separator),
	}

	s.Diff = diffview.Style{
		DividerLine: diffview.LineStyle{
			LineNumber: lipgloss.NewStyle().
				Foreground(o.fgSubtle).
				Background(o.bgLeastVisible),
			Code: lipgloss.NewStyle().
				Foreground(o.fgSubtle).
				Background(o.bgLeastVisible),
		},
		MissingLine: diffview.LineStyle{
			LineNumber: lipgloss.NewStyle().
				Background(o.bgLeastVisible),
			Code: lipgloss.NewStyle().
				Background(o.bgLeastVisible),
		},
		EqualLine: diffview.LineStyle{
			LineNumber: lipgloss.NewStyle().
				Foreground(o.fgMoreSubtle).
				Background(o.bgBase),
			Code: lipgloss.NewStyle().
				Foreground(o.fgMoreSubtle).
				Background(o.bgBase),
		},
		InsertLine: diffview.LineStyle{
			LineNumber: lipgloss.NewStyle().
				Foreground(lipgloss.Color("#629657")).
				Background(lipgloss.Color("#2b322a")),
			Symbol: lipgloss.NewStyle().
				Foreground(lipgloss.Color("#629657")).
				Background(lipgloss.Color("#323931")),
			Code: lipgloss.NewStyle().
				Background(lipgloss.Color("#323931")),
		},
		DeleteLine: diffview.LineStyle{
			LineNumber: lipgloss.NewStyle().
				Foreground(lipgloss.Color("#a45c59")).
				Background(lipgloss.Color("#312929")),
			Symbol: lipgloss.NewStyle().
				Foreground(lipgloss.Color("#a45c59")).
				Background(lipgloss.Color("#383030")),
			Code: lipgloss.NewStyle().
				Background(lipgloss.Color("#383030")),
		},
		Filename: diffview.LineStyle{
			LineNumber: lipgloss.NewStyle().
				Foreground(o.fgSubtle).
				Background(o.bgLeastVisible),
			Code: lipgloss.NewStyle().
				Foreground(o.fgSubtle).
				Background(o.bgLeastVisible),
		},
	}

	s.FilePicker = filepicker.Styles{
		DisabledCursor:   base.Foreground(o.fgMoreSubtle),
		Cursor:           base.Foreground(o.fgBase),
		Symlink:          base.Foreground(o.fgMostSubtle),
		Directory:        base.Foreground(o.fgBase).Bold(true),
		File:             base.Foreground(o.fgSubtle),
		DisabledFile:     base.Foreground(o.fgMoreSubtle),
		DisabledSelected: base.Background(o.bgMostVisible).Foreground(o.fgMoreSubtle),
		Permission:       base.Foreground(o.fgMoreSubtle),
		Selected:         base.Background(o.bgSelected).Foreground(o.fgBase).Bold(true),
		FileSize:         base.Foreground(o.fgMoreSubtle),
		EmptyDirectory:   base.Foreground(o.fgMoreSubtle).PaddingLeft(2).SetString("Empty directory"),
	}

	// borders
	s.ToolCallSuccess = lipgloss.NewStyle().Foreground(o.success).SetString(ToolSuccess)

	s.Header.WorkingDir = muted
	s.Header.Separator = subtle
	s.Header.Wrapper = lipgloss.NewStyle().Foreground(o.fgBase)
	s.Header.SessionTitle = lipgloss.NewStyle().Foreground(o.fgBase)
	s.Header.Breadcrumb = lipgloss.NewStyle().Foreground(o.fgMostSubtle)
	s.Header.LogoGradCanvas = lipgloss.NewStyle()
	s.Header.LogoGradFromColor = o.fgSubtle
	s.Header.LogoGradToColor = o.fgSubtle

	s.CompactDetails.Title = base
	s.CompactDetails.View = base.Padding(0, 1, 1, 1).Border(lipgloss.RoundedBorder()).BorderForeground(o.separator)
	s.CompactDetails.Version = lipgloss.NewStyle().Foreground(o.separator)

	// Tool rendering styles
	s.Tool.IconPending = base.Foreground(o.fgSubtle).SetString(ToolPending)
	s.Tool.IconSuccess = base.Foreground(o.fgMoreSubtle).SetString(ToolSuccess)
	s.Tool.IconError = base.Foreground(o.error).SetString(ToolError)
	s.Tool.IconCancelled = muted.SetString(ToolPending)

	s.Tool.NameNormal = base.Foreground(o.fgBase).Bold(true)
	s.Tool.NameNested = base.Foreground(o.fgBase).Bold(true)

	s.Tool.ParamMain = subtle
	s.Tool.ParamKey = subtle

	// Content rendering - prepared styles that accept width parameter
	s.Tool.ContentLine = muted.Background(o.bgLeastVisible)
	s.Tool.ContentTruncation = muted.Background(o.bgLeastVisible)
	s.Tool.ContentCodeLine = base.Background(o.bgBase).PaddingLeft(2)
	s.Tool.ContentCodeTruncation = muted.Background(o.bgBase).PaddingLeft(2)
	s.Tool.ContentCodeBg = o.bgBase
	s.Tool.Body = base.PaddingLeft(2)

	// Deprecated - kept for backward compatibility
	s.Tool.ContentBg = muted.Background(o.bgLeastVisible)
	s.Tool.ContentText = muted
	s.Tool.ContentLineNumber = base.Foreground(o.fgMoreSubtle).Background(o.bgBase).PaddingRight(1).PaddingLeft(1)

	s.Tool.StateWaiting = base.Foreground(o.fgMostSubtle)
	s.Tool.StateCancelled = base.Foreground(o.fgMostSubtle)

	// Status tags carry their meaning in the foreground: a solid chip would
	// be the loudest thing on a screen that is otherwise grayscale.
	s.Tool.ErrorTag = base.Padding(0, 1).Foreground(o.error).Bold(true)
	s.Tool.ErrorMessage = base.Foreground(o.fgSubtle)

	s.Tool.WarnTag = base.Padding(0, 1).Foreground(o.warning).Bold(true)
	s.Tool.WarnMessage = base.Foreground(o.fgSubtle)

	// Diff and multi-edit styles
	s.Tool.DiffTruncation = muted.Background(o.bgLeastVisible).PaddingLeft(2)
	s.Tool.NoteTag = base.Padding(0, 1).Foreground(o.fgMoreSubtle).Bold(true)
	s.Tool.NoteMessage = base.Foreground(o.fgSubtle)

	// Job header styles
	s.Tool.JobIconPending = base.Foreground(o.fgSubtle)
	s.Tool.JobIconError = base.Foreground(o.error)
	s.Tool.JobIconSuccess = base.Foreground(o.success)
	s.Tool.JobToolName = base.Foreground(o.fgBase).Bold(true)
	s.Tool.JobAction = base.Foreground(o.fgMoreSubtle)
	s.Tool.JobPID = muted
	s.Tool.JobDescription = subtle

	// Agent task styles
	s.Tool.AgentTaskTag = base.Bold(true).Padding(0, 1).MarginLeft(2).Foreground(o.fgMoreSubtle)
	s.Tool.AgentPrompt = muted

	// Todo styles
	s.Tool.TodoRatio = base.Foreground(o.fgMoreSubtle)
	s.Tool.TodoCompletedIcon = base.Foreground(o.success)
	s.Tool.TodoInProgressIcon = base.Foreground(o.fgBase)
	s.Tool.TodoPendingIcon = base.Foreground(o.fgMoreSubtle)
	s.Tool.TodoStatusNote = lipgloss.NewStyle().Foreground(o.fgMostSubtle)
	s.Tool.TodoItem = lipgloss.NewStyle().Foreground(o.fgBase)
	s.Tool.TodoJustStarted = lipgloss.NewStyle().Foreground(o.fgBase)

	// MCP styles
	s.Tool.MCPName = base.Foreground(o.fgBase).Bold(true)
	s.Tool.MCPToolName = base.Foreground(o.fgSubtle)
	s.Tool.MCPArrow = base.Foreground(o.fgMostSubtle).SetString(ArrowRightIcon)

	// Loading indicators for images, skills
	s.Tool.ResourceLoadedText = base.Foreground(o.fgSubtle)
	s.Tool.ResourceLoadedIndicator = base.Foreground(o.fgMoreSubtle)
	s.Tool.ResourceName = base
	s.Tool.MediaType = base
	s.Tool.ResourceSize = base.Foreground(o.fgMoreSubtle)

	// Hook styles
	s.Tool.HookLabel = base.Foreground(o.fgMoreSubtle)
	s.Tool.HookName = base
	s.Tool.HookMatcher = base.Foreground(o.fgMoreSubtle)
	s.Tool.HookArrow = base.Foreground(o.fgMostSubtle)
	s.Tool.HookDetail = base.Foreground(o.fgMoreSubtle)
	s.Tool.HookOK = base.Foreground(o.successMostSubtle)
	s.Tool.HookDenied = base.Foreground(o.error)
	s.Tool.HookDeniedLabel = base.Foreground(o.destructive)
	s.Tool.HookDeniedReason = base.Foreground(o.bgMostVisible)
	s.Tool.HookRewrote = base.Foreground(o.bgMostVisible)

	// Tool-call action verbs and result-list styling.
	s.Tool.ActionCreate = lipgloss.NewStyle().Foreground(o.successMoreSubtle)
	s.Tool.ActionDestroy = lipgloss.NewStyle().Foreground(o.destructive)
	s.Tool.ResultEmpty = lipgloss.NewStyle().Foreground(o.fgMostSubtle)
	s.Tool.ResultTruncation = lipgloss.NewStyle().Foreground(o.fgMostSubtle)
	s.Tool.ResultItemName = lipgloss.NewStyle().Foreground(o.fgBase)
	s.Tool.ResultItemDesc = lipgloss.NewStyle().Foreground(o.fgMostSubtle)

	// Buttons
	s.Button.Focused = lipgloss.NewStyle().Foreground(o.fgBase).Background(o.bgSelected).Bold(true)
	s.Button.Blurred = lipgloss.NewStyle().Foreground(o.fgMoreSubtle).Background(o.bgLessVisible)
	s.Button.Hovered = lipgloss.NewStyle().Foreground(o.fgBase).Background(o.bgMostVisible)
	s.Button.Negative = lipgloss.NewStyle().Foreground(o.error).Background(o.bgSelected).Bold(true)

	// Editor
	s.Editor.Rail = lipgloss.NewStyle().Foreground(o.fgMoreSubtle)
	s.Editor.RailBang = lipgloss.NewStyle().Foreground(o.busy)
	s.Editor.RailYolo = lipgloss.NewStyle().Foreground(o.warning)
	// Focus is a step on the gray ramp, not a change of hue. Brand color on
	// a full box border makes chrome the loudest thing on screen.
	s.Editor.Border = lipgloss.NewStyle().Foreground(o.separator)
	s.Editor.BorderFocused = lipgloss.NewStyle().Foreground(o.bgMostVisible)
	s.Editor.PromptMarkerFocused = lipgloss.NewStyle().Foreground(o.fgMoreSubtle)
	s.Editor.PromptMarkerBlurred = lipgloss.NewStyle().Foreground(o.fgMostSubtle)
	s.Editor.Caption = lipgloss.NewStyle().Foreground(o.fgMostSubtle)
	// No background: the label's own spaces erase the border glyphs, so it
	// reads as inset without painting a patch that differs from the cells
	// around it.
	s.Editor.BottomLabel = lipgloss.NewStyle().Foreground(o.fgMoreSubtle)
	s.Editor.PromptQuestionIconFocused = lipgloss.NewStyle().MarginRight(1).Foreground(o.fgBase).Background(o.bgSelected).Bold(true).SetString(" ? ")
	s.Editor.PromptQuestionIconBlurred = s.Editor.PromptQuestionIconFocused.Foreground(o.fgMoreSubtle).Background(o.bgLessVisible)
	s.Editor.QuestionSelected = lipgloss.NewStyle().Foreground(o.fgBase).Bold(true)
	s.Editor.QuestionUnselected = lipgloss.NewStyle().Foreground(o.fgMoreSubtle)
	s.Editor.QuestionBody = lipgloss.NewStyle().Foreground(o.fgMoreSubtle)
	s.Editor.QuestionConfirm = lipgloss.NewStyle().Foreground(o.fgBase).Bold(true)
	s.Editor.QuestionNote = lipgloss.NewStyle().Foreground(o.fgMostSubtle)
	s.Editor.QuestionCursorBar = lipgloss.NewStyle().Foreground(o.fgSubtle)
	s.Editor.QuestionRadioOn = lipgloss.NewStyle().Foreground(o.fgBase).SetString(RadioOn)
	s.Editor.QuestionRadioOff = lipgloss.NewStyle().Foreground(o.fgMostSubtle).SetString(RadioOff)
	s.Editor.QuestionCheckOn = lipgloss.NewStyle().Foreground(o.fgBase).SetString(RadioOn)
	s.Editor.QuestionCheckOff = lipgloss.NewStyle().Foreground(o.fgMostSubtle).SetString(RadioOff)

	s.Radio.On = lipgloss.NewStyle().Foreground(o.fgSubtle).SetString(RadioOn)
	s.Radio.Off = lipgloss.NewStyle().Foreground(o.fgSubtle).SetString(RadioOff)
	s.Radio.Label = lipgloss.NewStyle().Foreground(o.fgSubtle)

	// Tabs for batch question forms. Borders sit on the separator gray so a
	// form reads as chrome rather than as the focus of the screen. The
	// active tab has an open bottom that merges with the content area;
	// inactive tabs have a closed bottom. First tab gets a right-angle
	// bottom-left corner at draw time.
	borderColor := uv.Style{Fg: o.bgMostVisible}
	inactiveBorder := uv.RoundedBorder().Style(borderColor)
	inactiveBorder.BottomLeft = uv.Side{Content: "┴", Style: borderColor}
	inactiveBorder.BottomRight = uv.Side{Content: "┴", Style: borderColor}
	activeBorder := uv.RoundedBorder().Style(borderColor)
	activeBorder.Bottom = uv.Side{Content: " ", Style: borderColor}
	activeBorder.BottomLeft = uv.Side{Content: "┘", Style: borderColor}
	activeBorder.BottomRight = uv.Side{Content: "└", Style: borderColor}

	s.Tab.ActiveBorder = activeBorder
	s.Tab.InactiveBorder = inactiveBorder

	blurredBorderColor := uv.Style{Fg: o.fgMoreSubtle}
	inactiveBorderBlurred := uv.RoundedBorder().Style(blurredBorderColor)
	inactiveBorderBlurred.BottomLeft = uv.Side{Content: "┴", Style: blurredBorderColor}
	inactiveBorderBlurred.BottomRight = uv.Side{Content: "┴", Style: blurredBorderColor}
	activeBorderBlurred := uv.RoundedBorder().Style(blurredBorderColor)
	activeBorderBlurred.Bottom = uv.Side{Content: " ", Style: blurredBorderColor}
	activeBorderBlurred.BottomLeft = uv.Side{Content: "┘", Style: blurredBorderColor}
	activeBorderBlurred.BottomRight = uv.Side{Content: "└", Style: blurredBorderColor}
	s.Tab.ActiveBorderBlurred = activeBorderBlurred
	s.Tab.InactiveBorderBlurred = inactiveBorderBlurred

	s.Tab.ActiveStyle = uv.Style{Fg: o.fgBase}
	s.Tab.InactiveStyle = uv.Style{Fg: o.fgMoreSubtle}

	// Turn status
	s.TurnStatus.Spinner = base.Foreground(o.fgSubtle)
	s.TurnStatus.Activity = base.Foreground(o.fgBase)
	s.TurnStatus.Field = base.Foreground(o.fgMoreSubtle)
	s.TurnStatus.Separator = base.Foreground(o.fgMostSubtle)
	s.TurnStatus.HintKey = base.Foreground(o.fgMoreSubtle)
	s.TurnStatus.HintDesc = base.Foreground(o.fgMostSubtle)
	s.TurnStatus.Idle = base.Foreground(o.fgMostSubtle)

	// Logo. The wordmarks are the one place a brand gradient is allowed to
	// appear: the landing-page letterform wall runs across the teal family,
	// the header mark drifts from cyan toward a cyan-leaning blue. The far
	// end stops short of a true blue so the mark still reads as one hue.
	s.Logo.TitleColorA = o.primary
	s.Logo.TitleColorB = o.secondary
	s.Logo.VersionColor = o.fgMostSubtle
	s.Logo.GradCanvas = lipgloss.NewStyle()
	s.Logo.SmallGradFromColor = o.primary
	s.Logo.SmallGradToColor = o.infoMoreSubtle

	// Section
	s.Section.Title = subtle
	s.Section.Line = base.Foreground(o.separator)

	// Initialize
	s.Initialize.Header = base
	s.Initialize.Content = muted
	s.Initialize.Accent = base.Foreground(o.fgBase).Bold(true)

	// ResourceGroup (LSP/MCP/skills sidebar lists).
	s.Resource.Heading = lipgloss.NewStyle().Foreground(o.fgMostSubtle)
	s.Resource.Name = lipgloss.NewStyle().Foreground(o.fgMoreSubtle)
	s.Resource.StatusText = lipgloss.NewStyle().Foreground(o.fgMostSubtle)
	s.Resource.OfflineIcon = lipgloss.NewStyle().Foreground(o.bgMostVisible).SetString("●")
	s.Resource.BusyIcon = s.Resource.OfflineIcon.Foreground(o.busy)
	s.Resource.ErrorIcon = s.Resource.OfflineIcon.Foreground(o.destructive)
	s.Resource.OnlineIcon = s.Resource.OfflineIcon.Foreground(o.successMostSubtle)
	s.Resource.NeedsAuthIcon = s.Resource.OfflineIcon.Foreground(o.attention)
	s.Resource.DisabledIcon = lipgloss.NewStyle().Foreground(o.fgMoreSubtle).SetString("●")
	s.Resource.AdditionalText = lipgloss.NewStyle().Foreground(o.fgMostSubtle)
	s.Resource.CapabilityCount = lipgloss.NewStyle().Foreground(o.fgMostSubtle)
	s.Resource.RowTitleBase = lipgloss.NewStyle().Foreground(o.fgBase)
	s.Resource.RowDescBase = lipgloss.NewStyle().Foreground(o.fgBase)
	s.Resource.DefaultTitleFg = o.fgMoreSubtle
	s.Resource.DefaultDescFg = o.fgMostSubtle

	// LSP
	s.LSP.ErrorDiagnostic = base.Foreground(o.error)
	s.LSP.WarningDiagnostic = base.Foreground(o.warningSubtle)
	s.LSP.HintDiagnostic = base.Foreground(o.fgSubtle)
	s.LSP.InfoDiagnostic = base.Foreground(o.info)

	// Files
	s.Files.Path = lipgloss.NewStyle().Foreground(o.fgMoreSubtle)
	s.Files.Additions = lipgloss.NewStyle().Foreground(o.successMostSubtle)
	s.Files.Deletions = lipgloss.NewStyle().Foreground(o.error)
	s.Files.SectionTitle = lipgloss.NewStyle().Foreground(o.fgMostSubtle)
	s.Files.EmptyMessage = lipgloss.NewStyle().Foreground(o.fgMostSubtle)
	s.Files.TruncationHint = lipgloss.NewStyle().Foreground(o.fgMostSubtle)

	s.WorkingDirText = lipgloss.NewStyle().Foreground(o.fgMoreSubtle)
	s.Landing.Hint = lipgloss.NewStyle().Foreground(o.fgMostSubtle)
	s.Landing.MenuLabel = lipgloss.NewStyle().Foreground(o.fgSubtle)
	s.Landing.MenuKey = lipgloss.NewStyle().Foreground(o.fgMoreSubtle)

	// ModelInfo
	s.ModelInfo.Icon = lipgloss.NewStyle().Foreground(o.fgMostSubtle)
	s.ModelInfo.Name = lipgloss.NewStyle().Foreground(o.fgBase)
	s.ModelInfo.Provider = lipgloss.NewStyle().Foreground(o.fgMoreSubtle)
	s.ModelInfo.ProviderFallback = lipgloss.NewStyle().Foreground(o.fgMoreSubtle).PaddingLeft(2)
	s.ModelInfo.Reasoning = lipgloss.NewStyle().Foreground(o.fgMostSubtle).PaddingLeft(2)
	s.ModelInfo.TokenCount = lipgloss.NewStyle().Foreground(o.fgMostSubtle)
	s.ModelInfo.TokenPercentage = lipgloss.NewStyle().Foreground(o.fgMoreSubtle)
	s.ModelInfo.EstimatedUsagePrefix = s.ModelInfo.TokenPercentage
	s.ModelInfo.Cost = lipgloss.NewStyle().Foreground(o.fgMoreSubtle)

	// ResourceGroup
	s.Resource.DefaultTitleFg = o.fgMoreSubtle
	s.Resource.DefaultDescFg = o.fgMostSubtle

	// Chat
	messageFocussedBorder := lipgloss.Border{
		Left: "▌",
	}

	s.Messages.NoContent = lipgloss.NewStyle().Foreground(o.fgBase)
	s.Messages.UserBand = lipgloss.NewStyle().Background(o.bgLessVisible).Foreground(o.fgSubtle)
	s.Messages.UserBandPrompt = lipgloss.NewStyle().
		Background(o.bgLessVisible).Foreground(o.fgBase).Bold(true)
	s.Messages.UserBandTimestamp = lipgloss.NewStyle().
		Background(o.bgLessVisible).Foreground(o.fgMoreSubtle)
	s.Messages.UserBandAccentFocused = lipgloss.NewStyle().
		Background(o.bgLessVisible).Foreground(o.fgSubtle)
	s.Messages.UserBandAccentBlurred = lipgloss.NewStyle().
		Background(o.bgLessVisible).Foreground(o.fgMostSubtle)
	s.Messages.AssistantBlurred = s.Messages.NoContent.PaddingLeft(2)
	s.Messages.AssistantFocused = s.Messages.NoContent.PaddingLeft(1).BorderLeft(true).
		BorderForeground(o.fgMoreSubtle).BorderStyle(messageFocussedBorder)
	s.Messages.Thinking = lipgloss.NewStyle().MaxHeight(10)
	s.Messages.ErrorTag = lipgloss.NewStyle().Padding(0, 1).
		Foreground(o.error).Bold(true)
	s.Messages.ErrorTitle = lipgloss.NewStyle().Foreground(o.fgSubtle)
	s.Messages.ErrorDetails = lipgloss.NewStyle().Foreground(o.fgMostSubtle)

	// Message item styles
	s.Messages.ToolCallFocused = muted.PaddingLeft(1).
		BorderStyle(messageFocussedBorder).
		BorderLeft(true).
		BorderForeground(o.fgMostSubtle)
	s.Messages.ToolCallBlurred = muted.PaddingLeft(2)
	// No padding or border for compact tool calls within messages
	s.Messages.ToolCallCompact = muted

	// ANSI 16-color palette (indices 0-7 normal, 8-15 bright). Used to
	// remap raw terminal color codes in command output onto legible
	// colors. See [Styles.ANSI].
	s.ANSI = [16]color.Color{
		o.ansiBlack, o.ansiRed, o.ansiGreen, o.ansiYellow,
		o.ansiBlue, o.ansiMagenta, o.ansiCyan, o.ansiWhite,
		o.ansiBrightBlack, o.ansiBrightRed, o.ansiBrightGreen, o.ansiBrightYellow,
		o.ansiBrightBlue, o.ansiBrightMagenta, o.ansiBrightCyan, o.ansiBrightWhite,
	}

	// Shell (bang mode) item styles.
	s.Messages.ShellBarFocused = lipgloss.NewStyle().PaddingLeft(1).
		BorderStyle(messageFocussedBorder).BorderLeft(true).
		BorderForeground(o.busy)
	s.Messages.ShellBarBlurred = lipgloss.NewStyle().PaddingLeft(1).BorderLeft(true).
		BorderForeground(o.bgMostVisible).BorderStyle(lipgloss.NormalBorder())
	s.Messages.ShellPrompt = base.Foreground(o.busy).Bold(true)
	s.Messages.ShellPromptBlurred = base.Foreground(o.fgMoreSubtle)
	s.Messages.ShellCommand = base.Foreground(o.fgBase)
	s.Messages.ShellOutput = lipgloss.NewStyle().Foreground(o.fgSubtle)
	s.Messages.ShellExitCode = lipgloss.NewStyle().Foreground(o.destructive)
	s.Messages.ShellTruncation = muted

	s.Messages.SectionHeader = base.PaddingLeft(2)
	s.Messages.AssistantInfoIcon = subtle
	s.Messages.AssistantInfoModel = subtle
	s.Messages.AssistantInfoProvider = subtle
	s.Messages.AssistantInfoDuration = subtle
	s.Messages.AssistantCanceled = lipgloss.NewStyle().Foreground(o.fgSubtle).Italic(true)

	// Thinking section styles
	s.Messages.ThinkingBox = subtle.Background(o.bgLeastVisible)
	s.Messages.ThinkingTruncationHint = muted
	s.Messages.ThinkingFooterTitle = subtle
	s.Messages.ThinkingFooterDuration = subtle

	// Text selection.
	s.TextSelection = lipgloss.NewStyle().Foreground(o.fgBase).Background(o.bgMostVisible)

	// Dialog styles
	s.Dialog.Title = base.Padding(0, 1).Foreground(o.fgBase).Bold(true)
	s.Dialog.TitleText = base.Foreground(o.fgBase).Bold(true)
	s.Dialog.TitleError = base.Foreground(o.error).Bold(true)
	s.Dialog.TitleAccent = base.Foreground(o.fgSubtle).Bold(true)
	s.Dialog.TitleLineBase = lipgloss.NewStyle()
	// The rule after a dialog title is chrome, not brand: the title text
	// carries the accent, the rule stays dim.
	s.Dialog.TitleGradFromColor = o.separator
	s.Dialog.TitleGradToColor = o.separator

	// Dialog.ListItem (commands, reasoning, models). The info column holds
	// secondary hints like keybind shortcuts, so mute it when blurred and
	// keep it readable on the focused row.
	s.Dialog.ListItem.InfoBlurred = lipgloss.NewStyle().Foreground(o.fgMostSubtle)
	s.Dialog.ListItem.InfoFocused = lipgloss.NewStyle().Foreground(o.fgBase)

	// Dialog.Models
	s.Dialog.Models.ConfiguredText = lipgloss.NewStyle().Foreground(o.fgMostSubtle)

	// Dialog.Permissions
	s.Dialog.Permissions.KeyText = lipgloss.NewStyle().Foreground(o.fgMoreSubtle)
	s.Dialog.Permissions.ValueText = lipgloss.NewStyle().Foreground(o.fgBase)
	s.Dialog.Permissions.ParamsBg = o.bgLessVisible

	// Dialog.Quit
	s.Dialog.Quit.Content = lipgloss.NewStyle().Foreground(o.fgBase)
	s.Dialog.Quit.Hint = lipgloss.NewStyle().Foreground(o.fgMostSubtle)
	s.Dialog.Quit.Frame = lipgloss.NewStyle().BorderForeground(o.separator).Border(lipgloss.RoundedBorder()).Padding(1, 2)
	s.Dialog.View = base.Border(lipgloss.RoundedBorder()).BorderForeground(o.fgMostSubtle)
	s.Dialog.PrimaryText = base.Padding(0, 1).Foreground(o.fgBase).Bold(true)
	s.Dialog.SecondaryText = base.Padding(0, 1).Foreground(o.fgMostSubtle)
	s.Dialog.HelpView = base.Padding(0, 1).AlignHorizontal(lipgloss.Left)
	s.Dialog.Help.ShortKey = base.Foreground(o.fgSubtle).Bold(true)
	s.Dialog.Help.ShortDesc = base.Foreground(o.fgMostSubtle)
	s.Dialog.Help.ShortSeparator = base.Foreground(o.separator)
	s.Dialog.Help.Ellipsis = base.Foreground(o.separator)
	s.Dialog.Help.FullKey = base.Foreground(o.fgSubtle).Bold(true)
	s.Dialog.Help.FullDesc = base.Foreground(o.fgMostSubtle)
	s.Dialog.Help.FullSeparator = base.Foreground(o.separator)
	s.Dialog.NormalItem = base.Padding(0, 1).Foreground(o.fgBase)
	s.Dialog.SelectedItem = base.Padding(0, 1).Background(o.bgSelected).Foreground(o.fgBase).Bold(true)
	s.Dialog.InputPrompt = base.Margin(1, 1)

	s.Dialog.List = base.Margin(0, 0, 1, 0)
	s.Dialog.ContentPanel = base.Background(o.bgLessVisible).Foreground(o.fgBase).Padding(1, 2)
	s.Dialog.Spinner = base.Foreground(o.fgSubtle)
	s.Dialog.ScrollbarThumb = base.Foreground(o.fgMostSubtle)
	s.Dialog.ScrollbarTrack = base.Foreground(o.bgLessVisible)

	s.Dialog.ImagePreview = lipgloss.NewStyle().Padding(0, 1).Foreground(o.fgMostSubtle)

	// API key input dialog
	s.Dialog.APIKey.Spinner = base.Foreground(o.fgSubtle)

	// OAuth dialog
	s.Dialog.OAuth.Spinner = base.Foreground(o.fgSubtle)
	s.Dialog.OAuth.Instructions = lipgloss.NewStyle().Foreground(o.fgBase)
	s.Dialog.OAuth.UserCode = lipgloss.NewStyle().Bold(true).Foreground(o.fgBase)
	s.Dialog.OAuth.Success = lipgloss.NewStyle().Foreground(o.success)
	s.Dialog.OAuth.Link = lipgloss.NewStyle().Foreground(o.fgSubtle).Underline(true)
	s.Dialog.OAuth.Enter = lipgloss.NewStyle().Foreground(o.fgBase).Bold(true)
	s.Dialog.OAuth.ErrorText = lipgloss.NewStyle().Foreground(o.error)
	s.Dialog.OAuth.StatusText = lipgloss.NewStyle().Foreground(o.fgMoreSubtle)
	s.Dialog.OAuth.UserCodeBg = o.bgLeastVisible

	s.Dialog.Arguments.Content = base.Padding(1)
	s.Dialog.Arguments.Description = base.MarginBottom(1).MaxHeight(3)
	s.Dialog.Arguments.InputLabelBlurred = base.Foreground(o.fgMoreSubtle)
	s.Dialog.Arguments.InputLabelFocused = base.Bold(true)
	s.Dialog.Arguments.InputRequiredMarkBlurred = base.Foreground(o.fgMoreSubtle).SetString("*")
	s.Dialog.Arguments.InputRequiredMarkFocused = base.Foreground(o.fgBase).Bold(true).SetString("*")

	s.Dialog.Sessions.DeletingTitle = s.Dialog.Title.Foreground(o.error)
	s.Dialog.Sessions.DeletingView = s.Dialog.View.BorderForeground(o.error)
	s.Dialog.Sessions.DeletingMessage = base.Padding(1)
	s.Dialog.Sessions.DeletingTitleGradientFromColor = o.separator
	s.Dialog.Sessions.DeletingTitleGradientToColor = o.separator
	s.Dialog.Sessions.DeletingItemBlurred = s.Dialog.NormalItem.Foreground(o.fgMostSubtle)
	s.Dialog.Sessions.DeletingItemFocused = s.Dialog.SelectedItem.Background(o.bgSelected).Foreground(o.error)

	s.Dialog.Sessions.RenamingingTitle = s.Dialog.Title.Foreground(o.fgBase)
	s.Dialog.Sessions.RenamingView = s.Dialog.View.BorderForeground(o.fgMostSubtle)
	s.Dialog.Sessions.RenamingingMessage = base.Padding(1)
	s.Dialog.Sessions.RenamingTitleGradientFromColor = o.separator
	s.Dialog.Sessions.RenamingTitleGradientToColor = o.separator
	s.Dialog.Sessions.RenamingItemBlurred = s.Dialog.NormalItem.Foreground(o.fgMostSubtle)
	s.Dialog.Sessions.RenamingingItemFocused = s.Dialog.SelectedItem.UnsetBackground().UnsetForeground()
	s.Dialog.Sessions.RenamingPlaceholder = base.Foreground(o.fgMoreSubtle)
	s.Dialog.Sessions.InfoBlurred = lipgloss.NewStyle().Foreground(o.fgMostSubtle)
	s.Dialog.Sessions.InfoFocused = lipgloss.NewStyle().Foreground(o.fgBase)

	s.Status.Help = lipgloss.NewStyle().Padding(0, 1)

	// Notification chips. Severity lives in the label's foreground on a
	// neutral surface, so even an error notice stays a line of text rather
	// than a saturated bar across the status line.
	statusIndicator := base.Background(o.bgSelected).Padding(0, 1).Bold(true)
	statusMessage := base.Background(o.bgLessVisible).Padding(0, 1)

	s.Status.SuccessIndicator = statusIndicator.Foreground(o.success).SetString("ok")
	s.Status.InfoIndicator = statusIndicator.Foreground(o.fgBase).SetString("info")
	s.Status.UpdateIndicator = statusIndicator.Foreground(o.fgBase).SetString("new")
	s.Status.WarnIndicator = statusIndicator.Foreground(o.warning).SetString("warn")
	s.Status.ErrorIndicator = statusIndicator.Foreground(o.error).SetString("error")

	s.Status.SuccessMessage = statusMessage.Foreground(o.success)
	s.Status.InfoMessage = statusMessage.Foreground(o.fgSubtle)
	s.Status.UpdateMessage = statusMessage.Foreground(o.fgSubtle)
	s.Status.WarnMessage = statusMessage.Foreground(o.warning)
	s.Status.ErrorMessage = statusMessage.Foreground(o.error)

	// Completions styles
	s.Completions.Normal = base.Background(o.bgLessVisible).Foreground(o.fgSubtle)
	s.Completions.Focused = base.Background(o.bgSelected).Foreground(o.fgBase).Bold(true)
	s.Completions.Match = base.Underline(true)

	// Attachments styles
	attachmentIconStyle := base.Foreground(o.fgBase).Background(o.bgSelected).Padding(0, 1)
	s.Attachments.Image = attachmentIconStyle.SetString(ImageIcon)
	s.Attachments.Text = attachmentIconStyle.SetString(TextIcon)
	s.Attachments.Skill = attachmentIconStyle.SetString(SkillIcon)
	s.Attachments.Normal = base.Padding(0, 1).Background(o.bgMostVisible).Foreground(o.fgBase)
	// Remove and Deleting share the same slot on the right side of a chip
	// and must keep the same geometry so toggling delete-mode doesn't
	// shift the chips. Padding(0, 1) puts a colored cell on each side of the
	// glyph so it isn't flush against the box edge, while MarginRight(1)
	// keeps a transparent gap between adjacent chips.
	s.Attachments.Remove = base.Padding(0, 1).MarginRight(1).Background(o.bgLessVisible).Foreground(o.fgSubtle).SetString(RemoveIcon)
	s.Attachments.Deleting = base.Padding(0, 1).MarginRight(1).Bold(true).Background(o.bgSelected).Foreground(o.error)

	// Pills styles

	return s
}
