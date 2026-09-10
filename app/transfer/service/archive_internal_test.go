package service

// The source-preserving remapper is deliberately private; these tests exercise its text boundaries directly.
import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNoteRemappingChangesOnlyStructuralReferences(t *testing.T) {
	old := "pag_000000000000000000000000001"
	next := "pag_000000000000000000000000002"
	for _, tc := range []struct{ name, input, expected string }{
		{"multiline shape leaves label untouched", "<Shape\n page=\"" + old + "\" label=\"" + old + "\" />", "<Shape\n page=\"" + next + "\" label=\"" + old + "\" />"},
		{"fenced example stays unchanged", "```mdx\n<Shape page=\"" + old + "\" />\n```\n", "```mdx\n<Shape page=\"" + old + "\" />\n```\n"},
		{"inline example stays unchanged", "`<Shape page=\"" + old + "\" />`", "`<Shape page=\"" + old + "\" />`"},
		{"frontmatter links change while title remains", "---\ntitle: " + old + "\npages:\n- " + old + "\n---\n" + old, "---\ntitle: " + old + "\npages:\n- " + next + "\n---\n" + old},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.expected, remapNote(tc.input, func(string) string { return next }))
		})
	}
}

func TestDerivedIDsSurviveRetryButSeparateResourceKindsAndJobs(t *testing.T) {
	first, err := derived("job_000000000000000000000000001", "pag", "old-page")
	require.NoError(t, err)
	again, err := derived("job_000000000000000000000000001", "pag", "old-page")
	require.NoError(t, err)
	require.Equal(t, first, again)
	other, err := derived("job_000000000000000000000000002", "pag", "old-page")
	require.NoError(t, err)
	require.NotEqual(t, first, other)
	note, err := derived("job_000000000000000000000000001", "note", "old-page")
	require.NoError(t, err)
	require.NotEqual(t, strings.TrimPrefix(first, "pag_"), strings.TrimPrefix(note, "note_"))
}
