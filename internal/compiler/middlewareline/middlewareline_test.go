package middlewareline

import (
	"testing"

	"nautrouds/internal/compiler/tokenizer"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func tokenizeLines(t *testing.T, lines ...string) []*tokenizer.Part {
	t.Helper()
	tk := tokenizer.New()
	var all []*tokenizer.Part
	for i, line := range lines {
		parts, err := tk.Tokenize(i+1, line)
		require.NoError(t, err)
		all = append(all, parts...)
	}
	require.NoError(t, tk.Close())
	return all
}

func tokenizeOne(t *testing.T, line string) *tokenizer.Part {
	t.Helper()
	parts := tokenizeLines(t, line)
	require.Len(t, parts, 1)
	return parts[0]
}

func TestTryParse(t *testing.T) {
	t.Run("Builtin", func(t *testing.T) {
		tr := New()
		err := tr.TryParse(tokenizeOne(t, "$SetHeader(a, b)"))
		assert.NoError(t, err)
	})

	t.Run("UnknownBuiltin", func(t *testing.T) {
		tr := New()
		err := tr.TryParse(tokenizeOne(t, "$DoesNotExist(a)"))
		assert.Error(t, err)
	})

	t.Run("Mmfg", func(t *testing.T) {
		tr := New()
		err := tr.TryParse(tokenizeOne(t, "$mmfg(node)"))
		assert.NoError(t, err)
	})

	t.Run("InvalidMmfg", func(t *testing.T) {
		tr := New()
		err := tr.TryParse(tokenizeOne(t, "$mmfg()"))
		assert.Error(t, err)
	})

	t.Run("External", func(t *testing.T) {
		tr := New()
		err := tr.TryParse(tokenizeOne(t, "auth-service(/check, header=X-User-Id)"))
		assert.NoError(t, err)
	})

	t.Run("MultilineBuiltin", func(t *testing.T) {
		tr := New()
		parts := tokenizeLines(t, `  $SetHeader(`, `    "X-Forwarded-For",`, `    "Nautrouds"`, `  )`)
		require.Len(t, parts, 1)

		assert.NoError(t, tr.TryParse(parts[0]))
		assert.Equal(t, []string{`$SetHeader("X-Forwarded-For","Nautrouds")`}, tr.Flush())
	})

	t.Run("MultilineArgWithLiteralParens", func(t *testing.T) {
		tr := New()
		parts := tokenizeLines(t, `  $Log(`, `    "value (with parens)"`, `  )`)
		require.Len(t, parts, 1)

		assert.NoError(t, tr.TryParse(parts[0]))
	})

	t.Run("BodySizeLimitFollowedByBuiltin", func(t *testing.T) {
		tr := New()
		err := tr.TryParse(tokenizeOne(t, "$BodySizeLimit(1KB)"))
		assert.NoError(t, err)

		err = tr.TryParse(tokenizeOne(t, "$SetHeader(a, b)"))
		assert.Error(t, err)
	})

	t.Run("Accumulates", func(t *testing.T) {
		tr := New()
		_ = tr.TryParse(tokenizeOne(t, "$SetHeader(a, b)"))
		_ = tr.TryParse(tokenizeOne(t, "$IPAllow(10.0.0.0/8)"))
		assert.Equal(t, []string{"$SetHeader(a, b)", "$IPAllow(10.0.0.0/8)"}, tr.Flush())
	})
}

func TestFlush(t *testing.T) {
	tr := New()
	_ = tr.TryParse(tokenizeOne(t, "$SetHeader(a, b)"))

	assert.Equal(t, []string{"$SetHeader(a, b)"}, tr.Flush())
	assert.Empty(t, tr.Flush())
}
