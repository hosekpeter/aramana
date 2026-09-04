package ids

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewStringCreatesUUIDv7(t *testing.T) {
	t.Parallel()

	id, err := uuid.Parse(NewString())
	require.NoError(t, err)
	assert.Equal(t, uuid.RFC4122, id.Variant())
	assert.Equal(t, uuid.Version(7), id.Version())
}
