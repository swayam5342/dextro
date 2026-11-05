package s3

import (
	"context"
	"errors"
	"io"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	// Imported for its Docker-on-Windows fix.
	_ "github.com/swayam5342/dextro/internal/db/dbtest"
	"github.com/swayam5342/dextro/internal/netguard"
	"github.com/swayam5342/dextro/internal/provider"
	"github.com/swayam5342/dextro/internal/provider/providertest"
)

const (
	seaweedImage = "chrislusf/seaweedfs:4.52"
	testAccess   = "test-access-key"
	testSecret   = "test-secret-key"
)

// startSeaweed runs a real S3 server and returns its endpoint.
func startSeaweed(t *testing.T) (endpoint, hostPort string) {
	t.Helper()
	ctx := context.Background()
	container, err := testcontainers.Run(ctx, seaweedImage,
		testcontainers.WithCmd("server", "-dir=/data", "-s3", "-s3.port=8333"),
		testcontainers.WithEnv(map[string]string{
			"AWS_ACCESS_KEY_ID":     testAccess,
			"AWS_SECRET_ACCESS_KEY": testSecret,
		}),
		testcontainers.WithExposedPorts("8333/tcp"),
		testcontainers.WithWaitStrategy(wait.ForListeningPort("8333/tcp").WithStartupTimeout(2*time.Minute)),
	)
	testcontainers.CleanupContainer(t, container)
	if err != nil {
		t.Fatalf("start seaweedfs: %v", err)
	}
	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("seaweedfs host: %v", err)
	}
	port, err := container.MappedPort(ctx, "8333/tcp")
	if err != nil {
		t.Fatalf("seaweedfs port: %v", err)
	}
	hostPort = host + ":" + port.Port()
	return "http://" + hostPort, hostPort
}

func createBucket(t *testing.T, p *Provider, bucket string) {
	t.Helper()
	// The server may accept connections a moment before its S3 API is ready.
	var err error
	for range 60 {
		if _, err = p.client.CreateBucket(context.Background(), &awss3.CreateBucketInput{Bucket: aws.String(bucket)}); err == nil {
			return
		}
		time.Sleep(time.Second)
	}
	t.Fatalf("create bucket: %v", err)
}

func TestProvider(t *testing.T) {
	endpoint, hostPort := startSeaweed(t)
	guard := netguard.New([]string{hostPort})
	settings := Settings{Endpoint: endpoint, Region: "us-east-1", Bucket: "dextro-test"}
	credentials := Credentials{AccessKeyID: testAccess, SecretAccessKey: testSecret}

	open := func(t *testing.T, s Settings, c Credentials) *Provider {
		t.Helper()
		p, err := Open(s, c, guard.HTTPClient())
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		return p
	}
	p := open(t, settings, credentials)
	p.pageSize = providertest.ListPageSize
	createBucket(t, p, settings.Bucket)

	t.Run("conformance", func(t *testing.T) {
		providertest.Run(t, p)
	})

	t.Run("failed uploads leave no parts behind", func(t *testing.T) {
		body := io.MultiReader(io.LimitReader(zeros{}, partSize+1), failing{})
		if _, err := p.Put(context.Background(), provider.Upload{Name: "broken.bin", Body: body}); err == nil {
			t.Fatal("Put succeeded, want error")
		}
		out, err := p.client.ListMultipartUploads(context.Background(), &awss3.ListMultipartUploadsInput{Bucket: aws.String(settings.Bucket)})
		if err != nil {
			t.Fatalf("ListMultipartUploads: %v", err)
		}
		if len(out.Uploads) != 0 {
			t.Errorf("uploads left open: %d", len(out.Uploads))
		}
	})

	t.Run("objects outside dextro's area are out of reach", func(t *testing.T) {
		ctx := context.Background()
		const foreign = "someone-elses/file.txt"
		_, err := p.client.PutObject(ctx, &awss3.PutObjectInput{
			Bucket: aws.String(settings.Bucket),
			Key:    aws.String(foreign),
			Body:   strings.NewReader("theirs"),
		})
		if err != nil {
			t.Fatalf("PutObject: %v", err)
		}
		if _, err := p.Get(ctx, foreign, nil); !errors.Is(err, provider.ErrObjectNotFound) {
			t.Errorf("Get: error = %v, want ErrObjectNotFound", err)
		}
		if err := p.Delete(ctx, foreign); !errors.Is(err, provider.ErrObjectNotFound) {
			t.Errorf("Delete: error = %v, want ErrObjectNotFound", err)
		}
		page, err := p.List(ctx, "")
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		for _, object := range page.Objects {
			if object.ID == foreign {
				t.Error("the listing shows an object outside dextro's area")
			}
		}
		if _, err := p.client.HeadObject(ctx, &awss3.HeadObjectInput{Bucket: aws.String(settings.Bucket), Key: aws.String(foreign)}); err != nil {
			t.Errorf("the object was touched: %v", err)
		}
	})

	t.Run("uploads never set a public acl", func(t *testing.T) {
		object, err := p.Put(context.Background(), provider.Upload{Name: "private.txt", Body: strings.NewReader("x")})
		if err != nil {
			t.Fatalf("Put: %v", err)
		}
		t.Cleanup(func() { _ = p.Delete(context.Background(), object.ID) })
		acl, err := p.client.GetObjectAcl(context.Background(), &awss3.GetObjectAclInput{
			Bucket: aws.String(settings.Bucket), Key: aws.String(object.ID),
		})
		if err != nil {
			t.Skipf("server doesn't report ACLs: %v", err)
		}
		for _, grant := range acl.Grants {
			if grant.Grantee != nil && grant.Grantee.URI != nil && strings.Contains(*grant.Grantee.URI, "AllUsers") {
				t.Errorf("public grant: %+v", grant)
			}
		}
	})

	t.Run("memory use doesn't grow with the file", func(t *testing.T) {
		const size = 256 << 20
		// Measured against the part buffer, with room for the SDK and the runtime.
		const limit = 6 * partSize
		runtime.GC()
		var before runtime.MemStats
		runtime.ReadMemStats(&before)

		var peak atomic.Uint64
		done := make(chan struct{})
		sampled := make(chan struct{})
		go func() {
			defer close(sampled)
			for {
				select {
				case <-done:
					return
				case <-time.After(20 * time.Millisecond):
					var now runtime.MemStats
					runtime.ReadMemStats(&now)
					if now.HeapAlloc > peak.Load() {
						peak.Store(now.HeapAlloc)
					}
				}
			}
		}()
		object, err := p.Put(context.Background(), provider.Upload{Name: "big.bin", Body: io.LimitReader(zeros{}, size)})
		close(done)
		<-sampled
		if err != nil {
			t.Fatalf("Put: %v", err)
		}
		t.Cleanup(func() { _ = p.Delete(context.Background(), object.ID) })
		if object.Size != size {
			t.Errorf("Size = %d", object.Size)
		}
		if grew := peak.Load() - min(peak.Load(), before.HeapAlloc); grew > limit {
			t.Errorf("heap grew by %d MiB for a %d MiB file", grew>>20, size>>20)
		}
	})

	t.Run("a wrong secret fails the check", func(t *testing.T) {
		bad := open(t, settings, Credentials{AccessKeyID: testAccess, SecretAccessKey: "not-the-secret"})
		if err := bad.Check(context.Background()); err == nil {
			t.Fatal("Check succeeded, want error")
		}
	})

	t.Run("a missing bucket fails the check", func(t *testing.T) {
		missing := settings
		missing.Bucket = "no-such-bucket"
		if err := open(t, missing, credentials).Check(context.Background()); err == nil {
			t.Fatal("Check succeeded, want error")
		}
	})

	t.Run("the guard applies to the provider's own connections", func(t *testing.T) {
		p, err := Open(settings, credentials, netguard.New(nil).HTTPClient())
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		if err := p.Check(context.Background()); !errors.Is(err, netguard.ErrNotAllowed) {
			t.Errorf("error = %v, want ErrNotAllowed", err)
		}
	})
}

func TestErrorsDoNotCarryTheSecret(t *testing.T) {
	p, err := Open(
		Settings{Endpoint: "http://127.0.0.1:1", Region: "us-east-1", Bucket: "b"},
		Credentials{AccessKeyID: "visible-id", SecretAccessKey: "top-secret-value"},
		netguard.New([]string{"127.0.0.1:1"}).HTTPClient(),
	)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	err = p.Check(context.Background())
	if err == nil {
		t.Fatal("Check succeeded, want error")
	}
	if strings.Contains(err.Error(), "top-secret-value") {
		t.Errorf("error leaks the secret: %v", err)
	}
}

type zeros struct{}

func (zeros) Read(p []byte) (int, error) {
	clear(p)
	return len(p), nil
}

type failing struct{}

func (failing) Read([]byte) (int, error) { return 0, errors.New("client went away") }
