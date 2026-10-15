package provider

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/theimaginaryfoundation/what-iff/internal/models"
)

func TestTemperatureFor(t *testing.T) {
	t.Parallel()

	omitted := TemperatureFor(models.UtilityModelName, 0.2)
	require.False(t, omitted.Valid(), "utility model rejects temperature, so the param must be omitted")

	kept := TemperatureFor("gpt-4o", 0.2)
	require.True(t, kept.Valid())
	require.Equal(t, 0.2, kept.Value)

	unknown := TemperatureFor("some-unlisted-model", 0.4)
	require.True(t, unknown.Valid(), "models outside the catalog keep their temperature")
}
