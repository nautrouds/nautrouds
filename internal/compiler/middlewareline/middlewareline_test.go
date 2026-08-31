package middlewareline

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestTryParse(t *testing.T) {
	t.Run("Blank", func(t *testing.T) {
		tr := New()
		ok, err := tr.TryParse("")
		assert.False(t, ok)
		assert.NoError(t, err)
	})

	t.Run("TagLineIgnored", func(t *testing.T) {
		tr := New()
		ok, err := tr.TryParse("@no-metrics")
		assert.False(t, ok)
		assert.NoError(t, err)
	})

	t.Run("Builtin", func(t *testing.T) {
		tr := New()
		ok, err := tr.TryParse("$SetHeader(a, b)")
		assert.True(t, ok)
		assert.NoError(t, err)
	})

	t.Run("UnknownBuiltin", func(t *testing.T) {
		tr := New()
		ok, err := tr.TryParse("$DoesNotExist(a)")
		assert.True(t, ok)
		assert.Error(t, err)
	})

	t.Run("Mmfg", func(t *testing.T) {
		tr := New()
		ok, err := tr.TryParse("$mmfg(node)")
		assert.True(t, ok)
		assert.NoError(t, err)
	})

	t.Run("InvalidMmfg", func(t *testing.T) {
		tr := New()
		ok, err := tr.TryParse("$mmfg()")
		assert.True(t, ok)
		assert.Error(t, err)
	})

	t.Run("External", func(t *testing.T) {
		tr := New()
		ok, err := tr.TryParse("auth-service(/check, header=X-User-Id)")
		assert.True(t, ok)
		assert.NoError(t, err)
	})

	t.Run("UnclosedParenIsPending", func(t *testing.T) {
		tr := New()
		ok, err := tr.TryParse("auth-service(/check")
		assert.True(t, ok)
		assert.NoError(t, err)
		assert.True(t, tr.Pending())
	})

	t.Run("ExcessClosingParen", func(t *testing.T) {
		tr := New()
		ok, err := tr.TryParse("auth-service(/check))")
		assert.True(t, ok)
		assert.Error(t, err)
		assert.False(t, tr.Pending())
	})

	t.Run("MultilineBuiltin", func(t *testing.T) {
		tr := New()

		ok, err := tr.TryParse(`$SetHeader(`)
		assert.True(t, ok)
		assert.NoError(t, err)
		assert.True(t, tr.Pending())

		ok, err = tr.TryParse(`"X-Forwarded-For",`)
		assert.True(t, ok)
		assert.NoError(t, err)
		assert.True(t, tr.Pending())

		ok, err = tr.TryParse(`"Nautrouds"`)
		assert.True(t, ok)
		assert.NoError(t, err)
		assert.True(t, tr.Pending())

		ok, err = tr.TryParse(`)`)
		assert.True(t, ok)
		assert.NoError(t, err)
		assert.False(t, tr.Pending())

		assert.Equal(t, []string{`$SetHeader("X-Forwarded-For","Nautrouds")`}, tr.Flush())
	})

	t.Run("MultilineArgWithLiteralParens", func(t *testing.T) {
		tr := New()

		_, _ = tr.TryParse(`$Log(`)
		_, _ = tr.TryParse(`"value (with parens)"`)
		ok, err := tr.TryParse(`)`)
		assert.True(t, ok)
		assert.NoError(t, err)
		assert.False(t, tr.Pending())
	})

	t.Run("UnterminatedAtEOF", func(t *testing.T) {
		tr := New()
		_, _ = tr.TryParse(`$SetHeader(`)
		_, _ = tr.TryParse(`"X-Forwarded-For"`)
		assert.True(t, tr.Pending())
		assert.Empty(t, tr.Flush())
	})

	t.Run("BodySizeLimitFollowedByBuiltin", func(t *testing.T) {
		tr := New()
		ok, err := tr.TryParse("$BodySizeLimit(1KB)")
		assert.True(t, ok)
		assert.NoError(t, err)

		ok, err = tr.TryParse("$SetHeader(a, b)")
		assert.True(t, ok)
		assert.Error(t, err)
	})

	t.Run("Accumulates", func(t *testing.T) {
		tr := New()
		_, _ = tr.TryParse("$SetHeader(a, b)")
		_, _ = tr.TryParse("$IPAllow(10.0.0.0/8)")
		assert.Equal(t, []string{"$SetHeader(a, b)", "$IPAllow(10.0.0.0/8)"}, tr.Flush())
	})
}

func TestFlush(t *testing.T) {
	tr := New()
	_, _ = tr.TryParse("$SetHeader(a, b)")

	assert.Equal(t, []string{"$SetHeader(a, b)"}, tr.Flush())
	assert.Empty(t, tr.Flush())
}
