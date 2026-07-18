package rtk

const (
	rawCap                   = 10 * 1024 * 1024
	minCompressSize          = 500
	detectWindow             = 1024
	gitDiffHunkMaxLines      = 100
	gitLogMaxLines           = 200
	dedupLineMax             = 2000
	grepPerFileMax           = 10
	findPerDirMax            = 10
	findTotalDirMax          = 20
	statusMaxFiles           = 10
	statusMaxUntracked       = 10
	lsExtSummaryTop          = 5
	treeMaxLines             = 200
	searchListPerDirMax      = 10
	searchListTotalDirMax    = 20
	smartTruncateHead        = 120
	smartTruncateTail        = 60
	smartTruncateMinLines    = 250
	readNumberedMinHitRatio  = 0.7
)
