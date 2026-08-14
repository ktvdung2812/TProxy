package compression

import (
	"strings"

	"github.com/tproxy/tproxy/internal/canonical"
	"github.com/tproxy/tproxy/internal/rtk"
)

// Mode selects which compression engines run.
type Mode string

const (
	ModeOff     Mode = "off"
	ModeLite    Mode = "lite"
	ModeRTK     Mode = "rtk"
	ModeCaveman Mode = "caveman"
	ModeStacked Mode = "stacked"
	ModeFull    Mode = "full"
	ModeUltra   Mode = "ultra"
)

type Stats struct {
	Mode          Mode
	BytesBefore   int
	BytesAfter    int
	TokensSaved   int
	RTKSaved      int
	CavemanSaved  int
	CCRSaved      int
	HeadroomSaved int
	LLMSaved      int
}

// CompressRequest runs the configured compression pipeline (fail-open).
func CompressRequest(request *canonical.Request, mode Mode) Stats {
	if request == nil || mode == ModeOff {
		return Stats{Mode: mode}
	}
	stats := Stats{Mode: mode, BytesBefore: messageBytes(request)}
	for _, engine := range enginesForMode(mode) {
		switch engine {
		case "rtk":
			rtkStats := rtk.CompressRequest(request)
			stats.RTKSaved += rtkStats.TokensSaved
		case "ccr":
			stats.CCRSaved += applyCCR(request)
		case "headroom":
			stats.HeadroomSaved += applyHeadroom(request)
		case "llmlingua":
			stats.LLMSaved += applyLLMLingua(request)
		case "caveman":
			before := messageBytes(request)
			compressCavemanMessages(request)
			after := messageBytes(request)
			stats.CavemanSaved += estimateTokens(before - after)
		}
	}
	stats.BytesAfter = messageBytes(request)
	stats.TokensSaved = stats.RTKSaved + stats.CavemanSaved + stats.CCRSaved + stats.HeadroomSaved + stats.LLMSaved
	if stats.TokensSaved < 0 {
		stats.TokensSaved = 0
	}
	return stats
}

func enginesForMode(mode Mode) []string {
	switch mode {
	case ModeRTK:
		return []string{"rtk"}
	case ModeCaveman:
		return []string{"caveman"}
	case ModeStacked:
		return []string{"rtk", "caveman"}
	case ModeFull:
		return []string{"rtk", "ccr", "headroom", "caveman"}
	case ModeUltra:
		return []string{"rtk", "ccr", "headroom", "llmlingua", "caveman"}
	case ModeLite:
		return []string{"headroom"}
	default:
		return []string{"rtk"}
	}
}

func ParseMode(raw string) Mode {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", "lite":
		return ModeLite
	case "off", "false", "0":
		return ModeOff
	case "rtk":
		return ModeRTK
	case "caveman", "standard":
		return ModeCaveman
	case "stacked", "aggressive":
		return ModeStacked
	case "full":
		return ModeFull
	case "ultra":
		return ModeUltra
	default:
		return ModeLite
	}
}
