package hook

import "strings"

// lexer reads a shell command roughly: enough to find each simple command's
// words. It honours quotes and backslashes, keeps a `$(…)` or backtick
// substitution inside its word and also returns its text as a nested
// command, splits on ; & | ( ) and newlines, and drops heredoc bodies. It is
// no shell: an unclosed quote or substitution runs to the end.
type lexer struct {
	src      []rune
	i        int
	commands [][]string
	nested   []string
	words    []string
	word     strings.Builder
	inWord   bool     // the current word has started, even if empty (`""`)
	heredocs []string // delimiters whose bodies start after this line
	// A lexer reading the inside of `$(…)` stops at its closing paren.
	inSubstitution bool
	parens         int
	closed         bool
}

// split returns command's simple commands as words, and the text of every
// command substitution in it.
func split(command string) ([][]string, []string) {
	l := &lexer{src: []rune(command)}
	l.run()
	l.endCommand()
	return l.commands, l.nested
}

func (l *lexer) run() {
	for l.i < len(l.src) && !l.closed {
		l.step()
	}
}

func (l *lexer) peek(offset int) rune {
	if l.i+offset < len(l.src) {
		return l.src[l.i+offset]
	}
	return 0
}

func (l *lexer) step() {
	c := l.src[l.i]
	switch {
	case c == '\\' && l.peek(1) == '\n':
		l.i += 2
	case c == '\\' && l.i+1 < len(l.src):
		l.add(string(l.src[l.i+1]))
		l.i += 2
	case c == '\'':
		l.add(l.until('\''))
	case c == '"':
		l.i++
		l.doubleQuoted()
	case c == '$' && l.peek(1) == '(':
		l.substitution()
	case c == '`':
		l.backtick()
	case c == '<' && l.peek(1) == '<' && l.peek(2) != '<':
		l.heredoc()
	case c == '#' && !l.inWord:
		for l.i < len(l.src) && l.src[l.i] != '\n' {
			l.i++
		}
	case c == '\n':
		l.endCommand()
		l.i++
		l.skipHeredocBodies()
	case c == ')' && l.inSubstitution && l.parens == 0:
		l.endCommand()
		l.closed = true
		l.i++
	case c == '(' || c == ')':
		l.parens += map[rune]int{'(': 1, ')': -1}[c]
		l.endCommand()
		l.i++
	case strings.ContainsRune(";&|", c):
		l.endCommand()
		l.i++
	case c == ' ' || c == '\t':
		l.endWord()
		l.i++
	default:
		l.add(string(c))
		l.i++
	}
}

func (l *lexer) add(s string) {
	l.word.WriteString(s)
	l.inWord = true
}

func (l *lexer) endWord() {
	if l.inWord {
		l.words = append(l.words, l.word.String())
	}
	l.word.Reset()
	l.inWord = false
}

func (l *lexer) endCommand() {
	l.endWord()
	if len(l.words) > 0 {
		l.commands = append(l.commands, l.words)
	}
	l.words = nil
}

// until consumes from the opening rune at l.i to the closing one and returns
// what lies between.
func (l *lexer) until(closing rune) string {
	start := l.i + 1
	end := start
	for end < len(l.src) && l.src[end] != closing {
		end++
	}
	l.i = min(end+1, len(l.src))
	return string(l.src[start:end])
}

func (l *lexer) doubleQuoted() {
	l.inWord = true
	for l.i < len(l.src) && l.src[l.i] != '"' {
		switch c := l.src[l.i]; {
		case c == '\\' && l.peek(1) == '\n':
			l.i += 2
		case c == '\\' && l.i+1 < len(l.src):
			l.add(string(l.src[l.i+1]))
			l.i += 2
		case c == '$' && l.peek(1) == '(':
			l.substitution()
		case c == '`':
			l.backtick()
		default:
			l.add(string(c))
			l.i++
		}
	}
	l.i++
}

// substitution consumes a `$(…)`, finding its closing paren with a lexer of
// its own, so quotes, comments and heredocs inside it are honoured.
func (l *lexer) substitution() {
	start := l.i + 2
	sub := &lexer{src: l.src, i: start, inSubstitution: true}
	sub.run()
	end := sub.i
	if sub.closed {
		end--
	}
	inner := string(l.src[start:end])
	l.nested = append(l.nested, inner)
	l.add("$(" + inner + ")")
	l.i = sub.i
}

func (l *lexer) backtick() {
	inner := l.until('`')
	l.nested = append(l.nested, inner)
	l.add("`" + inner + "`")
}

// heredoc records the delimiter of a `<<` or `<<-` redirection; its body,
// which starts on the next line, is no command.
func (l *lexer) heredoc() {
	l.endWord()
	l.i += 2
	if l.peek(0) == '-' {
		l.i++
	}
	for l.peek(0) == ' ' || l.peek(0) == '\t' {
		l.i++
	}
	var delim strings.Builder
	for l.i < len(l.src) && !strings.ContainsRune(" \t\n;&|()<>", l.src[l.i]) {
		if c := l.src[l.i]; c != '\'' && c != '"' && c != '\\' {
			delim.WriteRune(c)
		}
		l.i++
	}
	l.heredocs = append(l.heredocs, delim.String())
}

func (l *lexer) skipHeredocBodies() {
	for _, delim := range l.heredocs {
		for l.i < len(l.src) {
			end := l.i
			for end < len(l.src) && l.src[end] != '\n' {
				end++
			}
			line := strings.TrimLeft(string(l.src[l.i:end]), "\t")
			l.i = min(end+1, len(l.src))
			if line == delim {
				break
			}
		}
	}
	l.heredocs = nil
}
