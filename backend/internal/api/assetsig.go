package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"net/url"
	"regexp"
	"strconv"
	"time"
)

// Signed asset URLs exist because a rendered note's images are fetched by the
// browser as plain <img src> subrequests, which cannot carry an
// "Authorization: Bearer" header. handlePubGetAsset's only other gate is
// pubRepoAccess, so on a guest-CLOSED repo every image in every note used to
// 404 with "repo not found" — for real repo members just as much as for
// share-link visitors, since neither one's browser can attach a token to an
// <img>.
//
// The credential a browser CAN carry is one in the URL itself. So the note-read
// handler, which has already authorized the caller via pubNoteAccess, mints a
// short-lived HMAC signature for each asset path that note references, and
// handlePubGetAsset accepts a valid signature in place of repo access.
//
// Each signature is scoped to ONE asset path in ONE repo and expires, so
// handing a note out via a share link never becomes a licence to enumerate the
// rest of that repo's attachments — the signature a share-link visitor holds
// only names the images their own note embeds.
const assetSigTTL = 12 * time.Hour

// themeCSSPath is the repo-scoped asset holding the exported Obsidian theme the
// reader injects around a rendered note.
const themeCSSPath = "_pubobs/obsidian.css"

// assetSrcInHTML matches an asset URL in a rendered note's src="" attribute.
// The path capture stops at ? and # so an already-signed URL is never
// double-signed, and at " so it never runs past the attribute. The repo-ID
// capture is deliberately looser than rewriteRepoID's strict UUID — that one has
// to positively identify a stale ID before replacing it, whereas this one only
// appends a signature, which is always issued against the request's own repoID.
var assetSrcInHTML = regexp.MustCompile(`(src=")/pub/([^/"]+)/assets/([^"?#]+)(")`)

// signAssetPath returns the base64url HMAC binding a repo, an asset path and an
// expiry together. assetPath must be in the same decoded form the handler reads
// out of the request path, not the percent-encoded form used inside HTML.
func signAssetPath(secret []byte, repoID, assetPath string, exp int64) string {
	mac := hmac.New(sha256.New, secret)
	// Newline separators keep the three fields unambiguous: a repo ID is a
	// UUID and validRepoPath rejects newlines, so neither can forge a boundary.
	mac.Write([]byte(repoID))
	mac.Write([]byte("\n"))
	mac.Write([]byte(assetPath))
	mac.Write([]byte("\n"))
	mac.Write([]byte(strconv.FormatInt(exp, 10)))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// assetSigQuery returns the "exp=…&sig=…" query string authorizing one asset.
func assetSigQuery(secret []byte, repoID, assetPath string, exp int64) string {
	return "exp=" + strconv.FormatInt(exp, 10) + "&sig=" + signAssetPath(secret, repoID, assetPath, exp)
}

// validAssetSig reports whether expStr/sig authorize repoID/assetPath right
// now. An unparseable or elapsed expiry fails, as does any signature mismatch.
func validAssetSig(secret []byte, repoID, assetPath, expStr, sig string) bool {
	if sig == "" || expStr == "" {
		return false
	}
	exp, err := strconv.ParseInt(expStr, 10, 64)
	if err != nil || time.Now().Unix() > exp {
		return false
	}
	want := signAssetPath(secret, repoID, assetPath, exp)
	return subtle.ConstantTimeCompare([]byte(sig), []byte(want)) == 1
}

// signAssetURLsInHTML appends a signature to every asset src in a rendered
// note, for the callers who receive server-decrypted html_content. URLs
// belonging to another repo, or already carrying a query, are left untouched.
func signAssetURLsInHTML(secret []byte, repoID, html string, exp int64) string {
	return assetSrcInHTML.ReplaceAllStringFunc(html, func(match string) string {
		g := assetSrcInHTML.FindStringSubmatch(match)
		if g[2] != repoID {
			return match
		}
		assetPath, err := url.PathUnescape(g[3])
		if err != nil {
			return match
		}
		return g[1] + "/pub/" + repoID + "/assets/" + g[3] +
			"?" + assetSigQuery(secret, repoID, assetPath, exp) + g[4]
	})
}

// signedAssetURLs maps each asset src in html to its signed equivalent, for the
// share-link-only visitor whose note HTML is decrypted in the browser and so
// cannot be rewritten here. The frontend applies the map to the DOM after
// decrypting. Keys are the exact src attribute values as they appear in html,
// so the client can look them up with getAttribute('src') without normalizing
// anything — including the stale repo IDs that rewriteRepoID exists to fix,
// which is why the captured ID is ignored here while every signature is issued
// against repoID. The paths signed are only ever ones this note itself embeds.
func signedAssetURLs(secret []byte, repoID, html string, exp int64) map[string]string {
	out := map[string]string{}
	for _, g := range assetSrcInHTML.FindAllStringSubmatch(html, -1) {
		assetPath, err := url.PathUnescape(g[3])
		if err != nil {
			continue
		}
		src := "/pub/" + g[2] + "/assets/" + g[3]
		out[src] = "/pub/" + repoID + "/assets/" + g[3] +
			"?" + assetSigQuery(secret, repoID, assetPath, exp)
	}
	return out
}
