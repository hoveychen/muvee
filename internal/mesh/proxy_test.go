package mesh

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type fakeCallers map[string]string

func (f fakeCallers) Lookup(_ context.Context, ip string) string { return f[ip] }

type fakeResolver struct {
	res   *Resolution
	err   error
	calls atomic.Int32
	got   [2]string
}

func (f *fakeResolver) Resolve(_ context.Context, caller, target string) (*Resolution, error) {
	f.calls.Add(1)
	f.got = [2]string{caller, target}
	return f.res, f.err
}

// meshRequest builds a request as seen by the gateway: from srcIP to Host.
func meshRequest(host, srcIP string) *http.Request {
	r := httptest.NewRequest("GET", "http://"+host+"/api/hello?x=1", nil)
	r.Host = host
	r.RemoteAddr = srcIP + ":40000"
	return r
}

func TestProxyForwardsWithCallerHeaders(t *testing.T) {
	var gotHost string
	var gotHeader http.Header
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost, gotHeader = r.Host, r.Header.Clone()
		io.WriteString(w, "pong "+r.URL.RequestURI())
	}))
	defer backend.Close()

	res := &fakeResolver{res: &Resolution{Allowed: true, CallerProjectID: "cid-1", CallerProjectName: "Caller App", TargetProjectID: "tid", Backend: strings.TrimPrefix(backend.URL, "http://")}}
	p := newProxy(fakeCallers{"10.213.128.5": "caller"}, res)

	req := meshRequest("target.muvee.internal", "10.213.128.5")
	req.Header.Set("X-Forwarded-User", "admin@evil")
	req.Header.Set("X-Forwarded-User-Email", "admin@evil")
	req.Header.Set("X-Muvee-Caller-Project", "spoofed")
	req.Header.Set("X-Muvee-Caller-Project-Id", "spoofed")
	req.Header.Set("X-Muvee-Caller-Whatever", "spoofed")
	req.Header.Set("Authorization", "Bearer keep-me")
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Code != 200 || rec.Body.String() != "pong /api/hello?x=1" {
		t.Fatalf("got %d %q", rec.Code, rec.Body.String())
	}
	if res.got != [2]string{"caller", "target"} {
		t.Fatalf("resolve called with %v", res.got)
	}
	if gotHost != "target.muvee.internal" {
		t.Errorf("Host not preserved: %q", gotHost)
	}
	if gotHeader.Get(HeaderCallerProject) != "Caller App" || gotHeader.Get(HeaderCallerProjectID) != "cid-1" {
		t.Errorf("caller headers wrong: %v", gotHeader)
	}
	if len(gotHeader.Values(HeaderCallerProject)) != 1 {
		t.Errorf("spoofed caller header survived: %v", gotHeader.Values(HeaderCallerProject))
	}
	for _, h := range []string{"X-Forwarded-User", "X-Forwarded-User-Email", "X-Muvee-Caller-Whatever"} {
		if v := gotHeader.Get(h); v != "" {
			t.Errorf("%s should be stripped, got %q", h, v)
		}
	}
	if gotHeader.Get("Authorization") != "Bearer keep-me" {
		t.Errorf("unrelated headers must pass through")
	}
}

func TestProxyErrorMapping(t *testing.T) {
	cases := []struct {
		name   string
		host   string
		src    string
		res    *Resolution
		err    error
		status int
	}{
		{"bad host", "example.com", "10.213.128.5", nil, nil, 404},
		{"nested host", "a.b.muvee.internal", "10.213.128.5", nil, nil, 404},
		{"unknown source ip", "t.muvee.internal", "10.213.128.99", nil, nil, 403},
		{"caller_not_found", "t.muvee.internal", "10.213.128.5", &Resolution{Reason: ReasonCallerNotFound}, nil, 404},
		{"target_not_found", "t.muvee.internal", "10.213.128.5", &Resolution{Reason: ReasonTargetNotFound}, nil, 404},
		{"different_owner", "t.muvee.internal", "10.213.128.5", &Resolution{Reason: ReasonDifferentOwner}, nil, 403},
		{"access_disabled", "t.muvee.internal", "10.213.128.5", &Resolution{Reason: ReasonAccessDisabled}, nil, 403},
		{"target_not_running", "t.muvee.internal", "10.213.128.5", &Resolution{Reason: ReasonTargetNotRunning}, nil, 503},
		{"unknown reason", "t.muvee.internal", "10.213.128.5", &Resolution{Reason: "something_new"}, nil, 403},
		{"control plane down", "t.muvee.internal", "10.213.128.5", nil, errors.New("dial"), 502},
		{"backend unreachable", "t.muvee.internal:80", "10.213.128.5", &Resolution{Allowed: true, Backend: "127.0.0.1:1"}, nil, 502},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := newProxy(fakeCallers{"10.213.128.5": "caller"}, &fakeResolver{res: c.res, err: c.err})
			rec := httptest.NewRecorder()
			p.ServeHTTP(rec, meshRequest(c.host, c.src))
			if rec.Code != c.status {
				t.Fatalf("status = %d, want %d (body %q)", rec.Code, c.status, rec.Body.String())
			}
		})
	}
}

func TestProxyWebSocketUpgrade(t *testing.T) {
	// Minimal upgrade-capable backend: hijacks and echoes one line.
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "websocket" || r.Header.Get(HeaderCallerProject) != "c" {
			http.Error(w, "bad upgrade", 400)
			return
		}
		conn, brw, _ := w.(http.Hijacker).Hijack()
		defer conn.Close()
		brw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		brw.Flush()
		line, _ := brw.ReadString('\n')
		brw.WriteString("echo:" + line)
		brw.Flush()
	}))
	defer backend.Close()

	p := newProxy(fakeCallers{"127.0.0.1": "caller"}, &fakeResolver{res: &Resolution{Allowed: true, CallerProjectName: "c", Backend: strings.TrimPrefix(backend.URL, "http://")}})
	gw := httptest.NewServer(p)
	defer gw.Close()

	conn, err := net.Dial("tcp", strings.TrimPrefix(gw.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	io.WriteString(conn, "GET /ws HTTP/1.1\r\nHost: t.muvee.internal\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
	br := bufio.NewReader(conn)
	resp, err := http.ReadResponse(br, nil)
	if err != nil || resp.StatusCode != 101 {
		t.Fatalf("upgrade failed: %v %v", resp, err)
	}
	io.WriteString(conn, "hi\n")
	line, _ := br.ReadString('\n')
	if line != "echo:hi\n" {
		t.Fatalf("got %q", line)
	}
}

func TestProxyStreamsWithoutBuffering(t *testing.T) {
	release := make(chan struct{})
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "first\n")
		w.(http.Flusher).Flush()
		<-release
		io.WriteString(w, "second\n")
	}))
	defer backend.Close()
	defer close(release)

	p := newProxy(fakeCallers{"127.0.0.1": "caller"}, &fakeResolver{res: &Resolution{Allowed: true, Backend: strings.TrimPrefix(backend.URL, "http://")}})
	gw := httptest.NewServer(p)
	defer gw.Close()

	req, _ := http.NewRequest("GET", gw.URL+"/sse", nil)
	req.Host = "t.muvee.internal"
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got := make(chan string, 1)
	go func() {
		line, _ := bufio.NewReader(resp.Body).ReadString('\n')
		got <- line
	}()
	select {
	case line := <-got:
		if line != "first\n" {
			t.Fatalf("got %q", line)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("first chunk was buffered by the gateway")
	}
}

func TestHTTPResolverContract(t *testing.T) {
	var gotQuery, gotSecret, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery, gotSecret = r.URL.Path, r.URL.RawQuery, r.Header.Get("X-Agent-Secret")
		io.WriteString(w, `{"allowed":true,"reason":"","caller_project_id":"c-uuid","caller_project_name":"Caller","target_project_id":"t-uuid","backend":"10.0.0.5:32768"}`)
	}))
	defer srv.Close()

	r := newHTTPResolver(srv.URL+"/", "s3cret")
	res, err := r.Resolve(context.Background(), "foo", "bar")
	if err != nil {
		t.Fatal(err)
	}
	if gotPath != "/api/agent/mesh/resolve" || gotQuery != "caller=foo&target=bar" || gotSecret != "s3cret" {
		t.Errorf("request: path=%q query=%q secret=%q", gotPath, gotQuery, gotSecret)
	}
	want := Resolution{Allowed: true, CallerProjectID: "c-uuid", CallerProjectName: "Caller", TargetProjectID: "t-uuid", Backend: "10.0.0.5:32768"}
	if *res != want {
		t.Errorf("got %+v", *res)
	}

	deny := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, `{"allowed":false,"reason":"access_disabled"}`)
	}))
	defer deny.Close()
	res, err = newHTTPResolver(deny.URL, "").Resolve(context.Background(), "a", "b")
	if err != nil || res.Allowed || res.Reason != ReasonAccessDisabled {
		t.Errorf("deny: %+v %v", res, err)
	}

	broken := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "nope", 401) }))
	defer broken.Close()
	if _, err := newHTTPResolver(broken.URL, "").Resolve(context.Background(), "a", "b"); err == nil {
		t.Error("non-200 must be an error")
	}
}

func TestCachedResolver(t *testing.T) {
	inner := &fakeResolver{res: &Resolution{Allowed: false, Reason: ReasonAccessDisabled}}
	c := newCachedResolver(inner, 5*time.Second)
	now := time.Unix(1000, 0)
	c.now = func() time.Time { return now }

	c.Resolve(context.Background(), "a", "b")
	c.Resolve(context.Background(), "a", "b")
	if inner.calls.Load() != 1 {
		t.Fatalf("second call within ttl should hit cache, calls=%d", inner.calls.Load())
	}
	c.Resolve(context.Background(), "a", "c")
	if inner.calls.Load() != 2 {
		t.Fatalf("different target must miss")
	}
	now = now.Add(6 * time.Second)
	c.Resolve(context.Background(), "a", "b")
	if inner.calls.Load() != 3 {
		t.Fatalf("expired entry must refetch")
	}

	inner.err, inner.res = errors.New("down"), nil
	if _, err := c.Resolve(context.Background(), "x", "y"); err == nil {
		t.Fatal("error should propagate")
	}
	inner.err, inner.res = nil, &Resolution{Allowed: true}
	if res, _ := c.Resolve(context.Background(), "x", "y"); !res.Allowed {
		t.Fatal("errors must not be cached")
	}
}

func TestDockerCallersRefresh(t *testing.T) {
	var lists atomic.Int32
	table := map[string]string{"10.0.0.3": "a"}
	d := &dockerCallers{ttl: time.Hour, minMissInterval: 0, list: func(context.Context) (map[string]string, error) {
		lists.Add(1)
		cp := map[string]string{}
		for k, v := range table {
			cp[k] = v
		}
		return cp, nil
	}}
	if d.Lookup(context.Background(), "10.0.0.3") != "a" {
		t.Fatal("initial lookup")
	}
	d.Lookup(context.Background(), "10.0.0.3")
	if lists.Load() != 1 {
		t.Fatalf("hit within ttl must not relist, lists=%d", lists.Load())
	}
	table["10.0.0.4"] = "b" // a new caller container starts
	if d.Lookup(context.Background(), "10.0.0.4") != "b" {
		t.Fatal("miss should refresh and find the new container")
	}
}

func TestDockerCallersInvalidate(t *testing.T) {
	table := map[string]string{"10.0.0.3": "a"}
	d := &dockerCallers{ttl: time.Hour, minMissInterval: time.Hour, list: func(context.Context) (map[string]string, error) {
		cp := map[string]string{}
		for k, v := range table {
			cp[k] = v
		}
		return cp, nil
	}}
	if d.Lookup(context.Background(), "10.0.0.3") != "a" {
		t.Fatal("initial lookup")
	}
	// Container a stops; an unlabelled container takes over its IP.
	delete(table, "10.0.0.3")
	d.invalidate() // network disconnect event
	if got := d.Lookup(context.Background(), "10.0.0.3"); got != "" {
		t.Fatalf("stale identity after invalidate: %q", got)
	}
}

func TestDockerCallersListErrorDropsTable(t *testing.T) {
	fail := false
	d := &dockerCallers{ttl: 0, minMissInterval: 0, list: func(context.Context) (map[string]string, error) {
		if fail {
			return nil, errors.New("docker down")
		}
		return map[string]string{"10.0.0.3": "a"}, nil
	}}
	d.Lookup(context.Background(), "10.0.0.3")
	fail = true
	if got := d.Lookup(context.Background(), "10.0.0.3"); got != "" {
		t.Fatalf("must not answer from an unrefreshable table, got %q", got)
	}
}
