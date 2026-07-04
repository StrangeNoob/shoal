package source

import "strings"

// NewTorlinkSources returns the full source set ported from torlink.
func NewTorlinkSources() []Source {
	return []Source{
		NewFitGirl(),
		NewYTS(),
		NewPirateBayMovies(),
		New1337xMovies(),
		NewEZTV(),
		NewSolidTorrents(),
		NewPirateBayTV(),
		New1337xTV(),
		NewNyaa(),
		NewSubsPlease(),
	}
}

// DefaultSources returns shoal's default provider set (archive + curated + torlink).
func DefaultSources() []Source {
	sources := []Source{NewArchive(), NewCurated()}
	return append(sources, NewTorlinkSources()...)
}

// NewDefault returns shoal's default multi-source catalogue.
func NewDefault() *MultiSource { return NewMulti(DefaultSources()...) }

// EnabledSources returns the default provider set minus any whose Name() appears
// in disabled (case-insensitive). Order is preserved. Unknown names are ignored.
func EnabledSources(disabled []string) []Source {
	if len(disabled) == 0 {
		return DefaultSources()
	}
	off := make(map[string]bool, len(disabled))
	for _, d := range disabled {
		off[strings.ToLower(strings.TrimSpace(d))] = true
	}
	var out []Source
	for _, s := range DefaultSources() {
		if !off[strings.ToLower(s.Name())] {
			out = append(out, s)
		}
	}
	return out
}
