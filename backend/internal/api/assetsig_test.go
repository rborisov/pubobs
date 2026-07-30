package api_test

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/pubobs/backend/internal/api"
)

// getPubAsset fetches an asset the way a browser fetches an <img src>: no
// Authorization header, credentials only ever in the URL.
func getPubAsset(deps *api.Deps, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("GET", "/pub/r1/assets/"+path, nil)
	rr := httptest.NewRecorder()
	api.BuildRouter(deps).ServeHTTP(rr, req)
	return rr
}

// seedAssetRepo builds a repo whose shared note docs/intro.md embeds the given
// HTML, encrypted under a real note key (unlike setupPubAccessTest's fixed
// placeholder key, which is not decryptable and so can't exercise the
// share-visitor asset path). Returns the note's share key.
func seedAssetRepo(t *testing.T, allowGuest bool, html string) (deps *api.Deps, key string) {
	t.Helper()
	deps, _ = newTestDepsForPub(t)
	ctx := context.Background()

	deps.Store.UpsertUser(ctx, "u1", "reader@x.com", "Reader")
	deps.Store.CreateRepo(ctx, "r1", "Test Repo", "https://x.com/r1.git", "", "main")
	require.NoError(t, deps.Store.SetRepoAllowGuest(ctx, "r1", allowGuest))
	require.NoError(t, deps.Store.GrantAccess(ctx, "a1", "r1", "user", "u1", "reader"))

	note, err := deps.Store.UpsertNote(ctx, "r1", "docs/intro.md")
	require.NoError(t, err)
	require.NoError(t, deps.Store.UpsertSnapshot(ctx, note.ID, "", "{}", "u1", "sha1"))

	key, err = deps.Store.GetOrCreateNoteKey(ctx, note.ID)
	require.NoError(t, err)
	require.NoError(t, deps.Store.SetNoteShared(ctx, note.ID, true, key))

	keyBytes, err := base64.RawURLEncoding.DecodeString(key)
	require.NoError(t, err)
	rstore, err := deps.Resolver.RenderStoreFor(ctx, "r1")
	require.NoError(t, err)
	require.NoError(t, rstore.Write("r1", "docs/intro.md", testGCMEncrypt(t, keyBytes, []byte(html))))

	return deps, key
}

// seedAssetTest sets up the shape that was broken: a repo whose shared note
// embeds assets/img.png, with the assets present in the asset store.
func seedAssetTest(t *testing.T, allowGuest bool) (deps *api.Deps, key string) {
	t.Helper()
	deps, key = seedAssetRepo(t, allowGuest, `<p>hi</p><img src="/pub/r1/assets/assets/img.png" alt="">`)

	astore, err := deps.Resolver.AssetStoreFor(context.Background(), "r1")
	require.NoError(t, err)
	require.NoError(t, astore.Write("r1", "assets/img.png", []byte("pngbytes")))
	require.NoError(t, astore.Write("r1", "assets/other.png", []byte("otherbytes")))
	require.NoError(t, astore.Write("r1", "_pubobs/obsidian.css", []byte("body{}")))
	return deps, key
}

func TestAsset_unsignedRequestOnGuestClosedRepoIs404(t *testing.T) {
	deps, _ := seedAssetTest(t, false)
	rr := getPubAsset(deps, "assets/img.png")
	require.Equal(t, http.StatusNotFound, rr.Code)
	require.Contains(t, rr.Body.String(), "repo not found")
}

func TestAsset_guestOpenRepoStillNeedsNoSignature(t *testing.T) {
	deps, _ := seedAssetTest(t, true)
	rr := getPubAsset(deps, "assets/img.png")
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	require.Equal(t, []byte("pngbytes"), rr.Body.Bytes())
}

// The regression this whole change exists for: a share-link visitor on a
// guest-closed repo can load the images their note embeds.
func TestAsset_shareLinkVisitorLoadsSignedAsset(t *testing.T) {
	deps, key := seedAssetTest(t, false)

	rr := getPubNote(deps, "?key="+url.QueryEscape(key))
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())

	var body struct {
		HTMLContent string            `json:"html_content"`
		AssetSigs   map[string]string `json:"asset_sigs"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	require.Empty(t, body.HTMLContent, "share visitor must still decrypt client-side")

	signed, ok := body.AssetSigs["/pub/r1/assets/assets/img.png"]
	require.True(t, ok, "expected a signature for the note's image, got %v", body.AssetSigs)

	u, err := url.Parse(signed)
	require.NoError(t, err)
	got := getPubAsset(deps, strings.TrimPrefix(u.Path, "/pub/r1/assets/")+"?"+u.RawQuery)
	require.Equal(t, http.StatusOK, got.Code, got.Body.String())
	require.Equal(t, []byte("pngbytes"), got.Body.Bytes())
}

// A signature names one asset path. Replaying it against a different path in the
// same repo must not work, or a share link would become a licence to enumerate
// every attachment in a private repo.
func TestAsset_signatureIsNotTransferableToAnotherPath(t *testing.T) {
	deps, key := seedAssetTest(t, false)

	rr := getPubNote(deps, "?key="+url.QueryEscape(key))
	var body struct {
		AssetSigs map[string]string `json:"asset_sigs"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	signed := body.AssetSigs["/pub/r1/assets/assets/img.png"]
	require.NotEmpty(t, signed)

	u, err := url.Parse(signed)
	require.NoError(t, err)

	replayed := getPubAsset(deps, "assets/other.png?"+u.RawQuery)
	require.Equal(t, http.StatusNotFound, replayed.Code, replayed.Body.String())
}

func TestAsset_tamperedSignatureRejected(t *testing.T) {
	deps, key := seedAssetTest(t, false)
	rr := getPubNote(deps, "?key="+url.QueryEscape(key))
	var body struct {
		AssetSigs map[string]string `json:"asset_sigs"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	u, err := url.Parse(body.AssetSigs["/pub/r1/assets/assets/img.png"])
	require.NoError(t, err)

	q := u.Query()
	for _, bad := range []string{"", "AAAA", q.Get("sig")[:len(q.Get("sig"))-1] + "x"} {
		got := getPubAsset(deps, "assets/img.png?exp="+q.Get("exp")+"&sig="+url.QueryEscape(bad))
		require.Equal(t, http.StatusNotFound, got.Code, "sig %q should be rejected", bad)
	}
}

func TestAsset_expiredSignatureRejected(t *testing.T) {
	deps, key := seedAssetTest(t, false)
	rr := getPubNote(deps, "?key="+url.QueryEscape(key))
	var body struct {
		AssetSigs map[string]string `json:"asset_sigs"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	u, err := url.Parse(body.AssetSigs["/pub/r1/assets/assets/img.png"])
	require.NoError(t, err)

	// Same signature, expiry rolled back to the past: the HMAC covers exp, so
	// this is both expired and (therefore) no longer a matching signature.
	got := getPubAsset(deps, "assets/img.png?exp=1&sig="+url.QueryEscape(u.Query().Get("sig")))
	require.Equal(t, http.StatusNotFound, got.Code, got.Body.String())
}

// A repo member reads server-decrypted html_content, so their signatures have
// to be baked into the HTML itself — their browser can't put a bearer token on
// an <img> any more than a share visitor's can.
func TestAsset_memberHTMLContentCarriesSignedSrcs(t *testing.T) {
	deps, _ := seedAssetTest(t, true)

	rr := getPubNote(deps, "")
	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var body struct {
		HTMLContent string `json:"html_content"`
		ThemeCSSURL string `json:"theme_css_url"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	require.NotEmpty(t, body.HTMLContent)

	m := regexp.MustCompile(`src="([^"]+)"`).FindStringSubmatch(body.HTMLContent)
	require.Len(t, m, 2, "expected a signed img src in %q", body.HTMLContent)
	require.Contains(t, m[1], "sig=")

	u, err := url.Parse(m[1])
	require.NoError(t, err)
	got := getPubAsset(deps, strings.TrimPrefix(u.Path, "/pub/r1/assets/")+"?"+u.RawQuery)
	require.Equal(t, http.StatusOK, got.Code, got.Body.String())

	// The theme is an asset the reader loads on its own, not via the note HTML.
	require.Contains(t, body.ThemeCSSURL, "_pubobs/obsidian.css")
	require.Contains(t, body.ThemeCSSURL, "sig=")
	tu, err := url.Parse(body.ThemeCSSURL)
	require.NoError(t, err)
	theme := getPubAsset(deps, strings.TrimPrefix(tu.Path, "/pub/r1/assets/")+"?"+tu.RawQuery)
	require.Equal(t, http.StatusOK, theme.Code, theme.Body.String())
}

// The signature covers the DECODED path, since that is what chi hands the
// handler — so a src with percent-encoding has to round-trip.
func TestAsset_percentEncodedPathSignsTheDecodedForm(t *testing.T) {
	deps, key := seedAssetRepo(t, false, `<img src="/pub/r1/assets/assets/home%20board.png">`)
	astore, err := deps.Resolver.AssetStoreFor(context.Background(), "r1")
	require.NoError(t, err)
	require.NoError(t, astore.Write("r1", "assets/home board.png", []byte("spaced")))

	rr := getPubNote(deps, "?key="+url.QueryEscape(key))
	var body struct {
		AssetSigs map[string]string `json:"asset_sigs"`
	}
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	signed, ok := body.AssetSigs["/pub/r1/assets/assets/home%20board.png"]
	require.True(t, ok, "got %v", body.AssetSigs)

	u, err := url.Parse(signed)
	require.NoError(t, err)
	got := getPubAsset(deps, "assets/home%20board.png?"+u.RawQuery)
	require.Equal(t, http.StatusOK, got.Code, got.Body.String())
	require.Equal(t, []byte("spaced"), got.Body.Bytes())
}
