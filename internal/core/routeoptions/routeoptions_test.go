package routeoptions_test

import (
	"testing"

	"nautrouds/internal/core/routeoptions"

	"github.com/stretchr/testify/assert"
)

func TestGetOptions_ReturnsZeroValue(t *testing.T) {
	o := routeoptions.GetOptions()
	defer routeoptions.PutOptions(o)

	assert.False(t, o.HasRetryLimit)
	assert.Zero(t, o.RetryLimit)
}

func TestSetRetryLimit(t *testing.T) {
	o := routeoptions.GetOptions()
	defer routeoptions.PutOptions(o)

	o.SetRetryLimit(5)

	assert.True(t, o.HasRetryLimit)
	assert.EqualValues(t, 5, o.RetryLimit)
}

func TestReset_ClearsRetryLimit(t *testing.T) {
	o := routeoptions.GetOptions()
	defer routeoptions.PutOptions(o)

	o.SetRetryLimit(5)
	o.Reset()

	assert.False(t, o.HasRetryLimit)
	assert.Zero(t, o.RetryLimit)
}

func TestPutOptions_ClearsBeforePoolReturn(t *testing.T) {
	o := routeoptions.GetOptions()
	o.SetRetryLimit(3)
	routeoptions.PutOptions(o)

	assert.False(t, o.HasRetryLimit)
	assert.Zero(t, o.RetryLimit)
}

func TestGetOptions_DoesNotLeakStateAcrossReuse(t *testing.T) {
	o1 := routeoptions.GetOptions()
	o1.SetRetryLimit(9)
	routeoptions.PutOptions(o1)

	o2 := routeoptions.GetOptions()
	defer routeoptions.PutOptions(o2)

	assert.False(t, o2.HasRetryLimit)
	assert.Zero(t, o2.RetryLimit)
}
