package tools

import (
	"slices"
	"strings"
)

// proposalLimit caps how many did-you-mean candidates an error carries.
const proposalLimit = 3

// proposals returns up to proposalLimit candidates closest to input by edit
// distance, closest first. Candidates beyond a similarity threshold are
// dropped entirely — a far-fetched proposal invites a confident wrong retry,
// which is worse than no proposal. A candidate related to the input by a
// path-suffix relation (one names the other's tail) counts as an exact hit:
// wrong path prefixes are the likeliest miss for agent-supplied file paths.
func proposals(input string, candidates []string) []string {
	type scored struct {
		value string
		dist  int
	}

	// The threshold scales with input length so long paths tolerate more
	// noise; the floor keeps short names matchable, where a quarter of the
	// length would forbid even a single-character typo.
	const (
		distanceFloor    = 2
		distanceFraction = 4
	)

	lowered := strings.ToLower(input)
	threshold := max(distanceFloor, len([]rune(input))/distanceFraction)

	var ranked []scored

	for _, cand := range candidates {
		lc := strings.ToLower(cand)

		dist := editDistance(lowered, lc)
		if strings.HasSuffix(lc, "/"+lowered) || strings.HasSuffix(lowered, "/"+lc) {
			dist = 0
		}

		if dist <= threshold {
			ranked = append(ranked, scored{value: cand, dist: dist})
		}
	}

	if len(ranked) == 0 {
		return nil
	}

	slices.SortStableFunc(ranked, func(a, b scored) int { return a.dist - b.dist })

	picked := min(proposalLimit, len(ranked))

	out := make([]string, 0, picked)
	for _, s := range ranked[:picked] {
		out = append(out, s.value)
	}

	return out
}

// editDistance is the rune-wise Levenshtein distance between a and b.
func editDistance(a, b string) int {
	ar, br := []rune(a), []rune(b)

	prev := make([]int, len(br)+1)
	for j := range prev {
		prev[j] = j
	}

	for i := 1; i <= len(ar); i++ {
		cur := make([]int, len(br)+1)

		cur[0] = i

		for j := 1; j <= len(br); j++ {
			cost := 1
			if ar[i-1] == br[j-1] {
				cost = 0
			}

			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}

		prev = cur
	}

	return prev[len(br)]
}
