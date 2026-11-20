package desktop

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

type updateTestTransport func(*http.Request) (*http.Response, error)

func (transport updateTestTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	return transport(request)
}

func TestCheckGitHubUpdate(t *testing.T) {
	const notes = "## 更新\n- 转发列表"
	const page = updateReleases + "/tag/v0.5.8.1"
	const releaseJSON = `{"tag_name":"v0.5.8.1","html_url":"` + page + `","body":"## 更新\n- 转发列表"}`
	client := &http.Client{Transport: updateTestTransport(func(request *http.Request) (*http.Response, error) {
		if request.Method != "GET" || request.URL.String() != "https://api.github.com/repos/LisonZhang0623/Meridian-Deck-Official/releases/latest" || request.Header.Get("Authorization") != "" {
			t.Error("expected an anonymous request to the public repository's latest release")
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(releaseJSON))}, nil
	})}
	var checker updateChecker
	release, err := checker.check(client, time.Now)
	if err != nil || release.Version != "0.5.8.1" || release.URL != page || release.Notes != notes {
		t.Fatalf("unexpected release %+v: %v", release, err)
	}
	for _, tc := range []struct {
		name string
		code int
		body string
		want string
	}{
		{"no release", http.StatusNotFound, "", "暂无"},
		{"untrusted URL", http.StatusOK, strings.Replace(releaseJSON, page, "https://example.com/", 1), "无效"},
		{"invalid fourth component", http.StatusOK, strings.ReplaceAll(releaseJSON, "v0.5.8.1", "v0.5.8.01"), "无效"},
		{"prerelease", http.StatusOK, strings.TrimSuffix(releaseJSON, "}") + `,"prerelease":true}`, "无效"},
		{"too large", http.StatusOK, strings.Repeat(" ", maxUpdateBody+1), "无法读取"},
		{"network error", 0, "", "无法连接"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: updateTestTransport(func(request *http.Request) (*http.Response, error) {
				if request.Header.Get("Authorization") != "" {
					t.Error("public release checks must not send credentials")
				}
				if tc.code == 0 {
					return nil, errors.New("transport failed")
				}
				return &http.Response{StatusCode: tc.code, Body: io.NopCloser(strings.NewReader(tc.body))}, nil
			})}
			var checker updateChecker
			if _, err := checker.check(client, time.Now); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestUpdateCooldownAndCache(t *testing.T) {
	const releaseJSON = `{"tag_name":"v0.5.6","html_url":"` + updateReleases + `/tag/v0.5.6"}`
	for _, tc := range []struct {
		name    string
		status  int
		body    string
		headers func(time.Time) http.Header
		wait    time.Duration
	}{
		{"primary quota reset", http.StatusForbidden, "", func(now time.Time) http.Header {
			return http.Header{"X-Ratelimit-Remaining": {"0"}, "X-Ratelimit-Reset": {strconv.FormatInt(now.Add(2*time.Minute).Unix(), 10)}}
		}, 2 * time.Minute},
		{"retry after seconds", http.StatusTooManyRequests, "", func(time.Time) http.Header {
			return http.Header{"Retry-After": {"30"}}
		}, 30 * time.Second},
		{"retry after date", http.StatusForbidden, "", func(now time.Time) http.Header {
			return http.Header{"Retry-After": {now.Add(45 * time.Second).UTC().Format(http.TimeFormat)}}
		}, 45 * time.Second},
		{"secondary limit without headers", http.StatusForbidden, `{"message":"You have exceeded a secondary rate limit."}`, func(time.Time) http.Header { return nil }, time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Unix(1_800_000_000, 0)
			clock := func() time.Time { return now }
			calls := 0
			client := &http.Client{Transport: updateTestTransport(func(request *http.Request) (*http.Response, error) {
				calls++
				if calls == 1 {
					return &http.Response{StatusCode: tc.status, Header: tc.headers(now), Body: io.NopCloser(strings.NewReader(tc.body))}, nil
				}
				return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(releaseJSON))}, nil
			})}
			var checker updateChecker
			_, err := checker.check(client, clock)
			var failure *updateCheckError
			if !errors.As(err, &failure) || failure.code != "UPDATE_RATE_LIMIT" || !failure.retryAt.Equal(now.Add(tc.wait)) {
				t.Fatalf("wrong cooldown: %#v", err)
			}
			now = now.Add(tc.wait - time.Second)
			if _, err := checker.check(client, clock); !errors.Is(err, failure) || calls != 1 {
				t.Fatalf("retry during cooldown made another request: %d, %v", calls, err)
			}
			now = now.Add(time.Second)
			if release, err := checker.check(client, clock); err != nil || release.Version != "0.5.6" || calls != 2 {
				t.Fatalf("retry at reset failed: %+v, %d, %v", release, calls, err)
			}
			if _, err := checker.check(client, clock); err != nil || calls != 2 {
				t.Fatalf("duplicate check missed successful cache: %d, %v", calls, err)
			}
		})
	}
}

func TestUpdateOpenAcceptsOnlyProjectReleasePages(t *testing.T) {
	s, _, _ := testService(t)
	for _, address := range []string{updateReleases, updateReleases + "/tag/v0.5.10", updateReleases + "/tag/v0.5.8.1"} {
		if result := s.Call("updates:open", callArguments(t, address)); !result.OK {
			t.Fatalf("open %s: %s", address, result.Error)
		}
	}
	for _, address := range []string{
		"https://github.com/LisonZhang0623/Meridian-Deck/releases/tag/v1.0.0", "https://example.com/", updateReleases + "/tag/v0.5.8.1?x=1",
		updateReleases + "/tag/v0.5.8.1-beta", updateReleases + "/tag/v0.5.8.1.2",
	} {
		if result := s.Call("updates:open", callArguments(t, address)); result.OK {
			t.Fatalf("accepted untrusted update URL %s", address)
		}
	}
}
