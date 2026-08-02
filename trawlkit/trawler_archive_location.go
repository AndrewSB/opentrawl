package trawlkit

// ResolvedTrawlerArchiveLocation is where a registered trawler's archive lives
// on this machine, together with the state root it sits under.
//
// Execution resolves these paths internally and hands them to the trawler. A
// host that has to reason about the archive as a file — to copy it, to check
// that it exists, or to place it under a different root — needs the same answer
// without running a command, which is what this exposes.
type ResolvedTrawlerArchiveLocation struct {
	StateRoot string
	TrawlerArchivePaths
}

// ResolveTrawlerArchiveLocation reports where the trawler's archive lives under
// this executor's state root, without executing anything.
func (e TrawlerExecutor) ResolveTrawlerArchiveLocation(trawler Trawler) (ResolvedTrawlerArchiveLocation, error) {
	paths, err := resolveTrawlerArchivePaths(e.opts.StateRoot, trawler.RegisteredTrawlerDeclaration())
	if err != nil {
		return ResolvedTrawlerArchiveLocation{}, err
	}
	return ResolvedTrawlerArchiveLocation{
		StateRoot:           paths.StateRoot,
		TrawlerArchivePaths: paths.TrawlerArchivePaths,
	}, nil
}
