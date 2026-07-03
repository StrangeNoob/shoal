package source

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
