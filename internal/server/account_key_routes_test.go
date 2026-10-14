package server

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/mux"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/theimaginaryfoundation/what-iff/internal/accountkeys"
	"github.com/theimaginaryfoundation/what-iff/internal/providerkeys"
)

func servesKeyRoutes(router *mux.Router) bool {
	var match mux.RouteMatch
	return router.Match(httptest.NewRequest(http.MethodGet, "/provider-keys", nil), &match)
}

// The key routes follow the resolver: served when requests take their keys
// from accounts, absent when the deployment supplies them.
func TestMountAccountKeyRoutes_FollowsTheChosenResolver(t *testing.T) {
	keys := accountkeys.New(nil, nil, zap.NewNop())

	router := mux.NewRouter()
	require.False(t, mountAccountKeyRoutes(router, providerkeys.NewDeploymentResolver(nil), keys))
	require.False(t, servesKeyRoutes(router))

	router = mux.NewRouter()
	require.True(t, mountAccountKeyRoutes(router, keys, keys))
	require.True(t, servesKeyRoutes(router))

	router = mux.NewRouter()
	require.False(t, mountAccountKeyRoutes(router, accountkeys.New(nil, nil, zap.NewNop()), keys), "another account store is not the one being served")
	require.False(t, servesKeyRoutes(router))
}

// With the default New, the server takes the deployment's keys and serves no
// key routes.
func TestDefaultResolverServesNoKeyRoutes(t *testing.T) {
	keys := accountkeys.New(nil, nil, zap.NewNop())
	resolver := providerkeys.New(providerkeys.NewDeploymentResolver(nil), keys)

	router := mux.NewRouter()
	require.False(t, mountAccountKeyRoutes(router, resolver, keys))
	require.False(t, servesKeyRoutes(router))
}
