package tools

import "dev.gaijin.team/go/go-gerrit-mcp/internal/activity"

// ReviewActivityBlock exposes renderReviewActivity to the black-box golden
// tests.
func ReviewActivityBlock(d *activity.Delta) string {
	return renderReviewActivity(d)
}

// ReviewActivitySummary exposes summarizeReviewActivity to the black-box
// tests.
func ReviewActivitySummary(d *activity.Delta) string {
	return summarizeReviewActivity(d)
}

// InaccessibleBlock exposes renderInaccessible to the black-box golden
// tests.
func InaccessibleBlock(change int, reason string) string {
	return renderInaccessible(change, reason)
}
