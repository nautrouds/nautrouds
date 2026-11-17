package routeoptions_test

import (
	"testing"

	"nautrouds/internal/core/routeoptions"

	"github.com/stretchr/testify/assert"
)

func TestGetOptions_DoesNotLeakStateAcrossReuse(t *testing.T) {
	o1 := routeoptions.GetOptions()
	o1.SetRetryLimit(9)
	routeoptions.PutOptions(o1)

	o2 := routeoptions.GetOptions()
	defer routeoptions.PutOptions(o2)

	assert.False(t, o2.HasRetryLimit)
	assert.Zero(t, o2.RetryLimit)
}
