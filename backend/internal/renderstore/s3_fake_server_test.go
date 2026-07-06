// backend/internal/renderstore/s3_fake_server_test.go
package renderstore_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/pubobs/backend/internal/renderstore"
	"github.com/stretchr/testify/require"
)

// fakeS3ListServer serves just enough of the ListObjectsV2 API for
// S3RenderStore.ListAllObjects to exercise the real minio-go client against
// it. minio-go requests GET /{bucket}/ (trailing slash) — register the
// pattern with the slash, or it silently 404s.
func fakeS3ListServer(t *testing.T, bucket string, keys []string, sizes []int64) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/"+bucket+"/", func(w http.ResponseWriter, r *http.Request) {
		var contents strings.Builder
		for i, k := range keys {
			contents.WriteString(fmt.Sprintf(
				`<Contents><Key>%s</Key><LastModified>2024-01-01T00:00:00.000Z</LastModified><ETag>&quot;etag%d&quot;</ETag><Size>%d</Size><StorageClass>STANDARD</StorageClass></Contents>`,
				k, i, sizes[i],
			))
		}
		w.Header().Set("Content-Type", "application/xml")
		fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>
<ListBucketResult xmlns="http://s3.amazonaws.com/doc/2006-03-01/">
<Name>%s</Name><Prefix></Prefix><KeyCount>%d</KeyCount><MaxKeys>1000</MaxKeys><IsTruncated>false</IsTruncated>
%s
</ListBucketResult>`, bucket, len(keys), contents.String())
	})
	return httptest.NewServer(mux)
}

func TestS3RenderStore_ListAllObjects(t *testing.T) {
	srv := fakeS3ListServer(t, "test-bucket",
		[]string{"renders/repo1/a.md.enc", "assets/repo1/img.png.enc"},
		[]int64{100, 4000},
	)
	defer srv.Close()

	endpoint := strings.TrimPrefix(srv.URL, "http://")
	store, err := renderstore.NewS3(endpoint, "test-bucket", "ak", "sk", "us-east-1", false, "")
	require.NoError(t, err)

	objects, err := store.ListAllObjects()
	require.NoError(t, err)
	require.Len(t, objects, 2)

	require.Equal(t, int64(100), renderstore.SumObjectSizesWithPrefix(objects, "renders/"))
	require.Equal(t, int64(4000), renderstore.SumObjectSizesWithPrefix(objects, "assets/"))
}
