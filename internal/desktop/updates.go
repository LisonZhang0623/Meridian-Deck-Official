package desktop

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	updateRepository = "LisonZhang0623/Meridian-Deck-Official"
	updateReleases   = "https://github.com/" + updateRepository + "/releases"
	updateLatestAPI  = "https://api.github.com/repos/" + updateRepository + "/releases/latest"
	maxUpdateBody    = 1 << 20
)

var (
	updateVersion = regexp.MustCompile(`^v(0|[1-9][0-9]{0,8})(\.(0|[1-9][0-9]{0,8})){2,3}$`)
	updateClient  = &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
)

type updateRelease struct {
	Version string `json:"version"`
	URL     string `json:"url"`
	Notes   string `json:"notes"`
}

type updateCheckError struct {
	code    string
	message string
	retryAt time.Time
}

func (e *updateCheckError) Error() string { return e.message }

type updateChecker struct {
	mu         sync.Mutex
	release    updateRelease
	validUntil time.Time
	limited    *updateCheckError
}

func (c *updateChecker) check(client *http.Client, now func() time.Time) (updateRelease, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if current := now(); current.Before(c.validUntil) {
		return c.release, nil
	} else if c.limited != nil && current.Before(c.limited.retryAt) {
		return updateRelease{}, c.limited
	}
	release, err := checkGitHubUpdate(client, now)
	if err != nil {
		if limit, ok := err.(*updateCheckError); ok && limit.code == "UPDATE_RATE_LIMIT" {
			c.limited = limit
		}
		return updateRelease{}, err
	}
	c.release, c.validUntil, c.limited = release, now().Add(time.Minute), nil
	return release, nil
}

func updateRateLimit(headers http.Header, now time.Time) *updateCheckError {
	var retryAt time.Time
	if seconds, err := strconv.ParseInt(headers.Get("Retry-After"), 10, 64); err == nil && seconds > 0 && seconds <= int64((1<<63-1)/time.Second) {
		retryAt = now.Add(time.Duration(seconds) * time.Second)
	} else if date, err := http.ParseTime(headers.Get("Retry-After")); err == nil {
		retryAt = date
	}
	if headers.Get("X-RateLimit-Remaining") == "0" {
		if unix, err := strconv.ParseInt(headers.Get("X-RateLimit-Reset"), 10, 64); err == nil && time.Unix(unix, 0).After(retryAt) {
			retryAt = time.Unix(unix, 0)
		}
	}
	if !retryAt.After(now) {
		retryAt = now.Add(time.Minute)
	}
	message := fmt.Sprintf("GitHub 暂时限制了更新检查。请在 %s 后重试。", retryAt.Local().Format("01-02 15:04:05"))
	return &updateCheckError{code: "UPDATE_RATE_LIMIT", message: message, retryAt: retryAt}
}

func secondaryUpdateRateLimit(body io.Reader) bool {
	var failure struct{ Message string }
	return json.NewDecoder(io.LimitReader(body, 8192)).Decode(&failure) == nil && strings.Contains(strings.ToLower(failure.Message), "secondary rate limit")
}

func validUpdateURL(value string) bool {
	if value == updateReleases {
		return true
	}
	tag, ok := strings.CutPrefix(value, updateReleases+"/tag/")
	return ok && updateVersion.MatchString(tag)
}

func checkGitHubUpdate(client *http.Client, now func() time.Time) (updateRelease, error) {
	request, err := http.NewRequest(http.MethodGet, updateLatestAPI, nil)
	if err != nil {
		return updateRelease{}, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2026-03-10")
	request.Header.Set("User-Agent", "Meridian-Deck-Update")
	response, err := client.Do(request)
	if err != nil {
		return updateRelease{}, &updateCheckError{code: "UPDATE_NETWORK", message: "无法连接 GitHub，请检查网络后重试。"}
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusForbidden, http.StatusTooManyRequests:
		if response.StatusCode == http.StatusTooManyRequests || response.Header.Get("X-RateLimit-Remaining") == "0" || response.Header.Get("Retry-After") != "" || secondaryUpdateRateLimit(response.Body) {
			return updateRelease{}, updateRateLimit(response.Header, now())
		}
		return updateRelease{}, errors.New("GitHub 暂时拒绝了更新检查，可打开公开发布页查看")
	case http.StatusNotFound:
		return updateRelease{}, errors.New("公开仓库暂无可查看的正式版")
	default:
		return updateRelease{}, fmt.Errorf("GitHub 更新检查失败（HTTP %d）", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxUpdateBody+1))
	if err != nil || len(data) > maxUpdateBody {
		return updateRelease{}, errors.New("无法读取 GitHub 发布信息，请稍后重试")
	}
	var release struct {
		Tag        string `json:"tag_name"`
		URL        string `json:"html_url"`
		Notes      string `json:"body"`
		Draft      bool   `json:"draft"`
		Prerelease bool   `json:"prerelease"`
	}
	if json.Unmarshal(data, &release) != nil || release.Draft || release.Prerelease || !updateVersion.MatchString(release.Tag) || release.URL != updateReleases+"/tag/"+release.Tag {
		return updateRelease{}, errors.New("GitHub 返回了无效的正式发布信息")
	}
	return updateRelease{Version: strings.TrimPrefix(release.Tag, "v"), URL: release.URL, Notes: release.Notes}, nil
}

func (s *Service) callUpdate(op string, args []json.RawMessage) (any, error) {
	if op == "updates:check" {
		if err := decode(args); err != nil {
			return nil, err
		}
		return s.updates.check(updateClient, time.Now)
	}
	var address string
	if err := decode(args, &address); err != nil {
		return nil, err
	}
	if !validUpdateURL(address) {
		return nil, errors.New("无效的 Meridian Deck GitHub 发布地址")
	}
	return nil, s.ui.OpenURL(address)
}
