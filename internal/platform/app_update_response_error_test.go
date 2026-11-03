package platform

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"silverwing-go/internal/storage"
)

func TestGithubReleaseRateLimitExplainsQuotaAndRetryTime(t *testing.T) {
	until := time.Now().Add(30 * time.Minute).Truncate(time.Second)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(until.Unix(), 10))
		w.WriteHeader(http.StatusForbidden)
		json.NewEncoder(w).Encode(map[string]string{"message": "API rate limit exceeded for fixture"})
	}))
	defer server.Close()
	_, err := releaseTestChecker(server).check(context.Background())
	if err == nil || !strings.Contains(err.Error(), "匿名请求额度已用完") || !strings.Contains(err.Error(), until.Local().Format("2006-01-02 15:04:05")) {
		t.Fatalf("unclear limit: %v", err)
	}
}

func TestGithubReleaseForbiddenIsNotAutomaticallyRateLimited(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
		io.WriteString(w, `{"message":"Resource not accessible"}`)
	}))
	defer server.Close()
	_, err := releaseTestChecker(server).check(context.Background())
	if err == nil || !strings.Contains(err.Error(), "拒绝访问") || !strings.Contains(err.Error(), "403") || strings.Contains(err.Error(), "额度") {
		t.Fatal("403 was misclassified", err)
	}
}

func TestGithubReleaseBackoffAvoidsRepeatedRequests(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "120")
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	defer server.Close()
	s := NewService(storage.NewPaths(t.TempDir()), nil, nil)
	s.updateChecker = releaseTestChecker(server)
	for range 2 {
		if _, err := s.checkApplicationUpdate(context.Background()); err == nil {
			t.Fatal("rate limit accepted")
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("repeated check ignored backoff: %d requests", calls.Load())
	}
}

func TestGithubReleaseBackoffUsesServerRetryHeadersAndExpires(t *testing.T) {
	now := time.Date(2026, 11, 3, 5, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		retry, reset string
		quota        bool
		seconds      int
	}{
		{"120", "", false, 120},
		{"172800", "", false, 172800},
		{now.Add(90 * time.Second).Format(http.TimeFormat), "", false, 90},
		{"", strconv.FormatInt(now.Add(10*time.Minute).Unix(), 10), true, 600},
		{"15", strconv.FormatInt(now.Add(5*time.Minute).Unix(), 10), true, 300},
		{"bad", "past", false, 60},
	} {
		header := http.Header{}
		header.Set("Retry-After", test.retry)
		header.Set("X-RateLimit-Reset", test.reset)
		actual := githubReleaseRetryAt(header, now, test.quota)
		if !actual.Equal(now.Add(time.Duration(test.seconds) * time.Second)) {
			t.Fatalf("wrong retry deadline: %s", actual)
		}
	}
	b := &appUpdateCheckBackoff{}
	b.record("original", &releaseRequestError{reason: "limited", retryAt: now.Add(time.Minute)})
	if b.pending("original", now) == nil {
		t.Fatal("backoff missing")
	}
	if b.pending("new route", now) != nil || b.pending("original", now) == nil {
		t.Fatal("cooldown was not isolated by user-selected route")
	}
	if b.pending("original", now.Add(time.Minute)) != nil {
		t.Fatal("expired limit still blocks checking")
	}
	b.record("original", fmt.Errorf("network error"))
	if b.pending("original", now) != nil {
		t.Fatal("network error received a quota cooldown")
	}
}
