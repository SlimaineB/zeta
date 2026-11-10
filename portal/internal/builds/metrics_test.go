package builds

import (
	"testing"

	"zeta/portal/internal/domain"
)

func TestParseMetrics(t *testing.T) {
	log := "building...\n" +
		"ZETA_METRIC ccache_hits=7\n" +
		"ZETA_METRIC ccache_misses=3\n" +
		"ZETA_METRIC cache_bytes=1048576\n" +
		"ZETA_METRIC uv_packages=14\n"
	got := ParseMetrics(log)
	if got.Hits != 7 || got.Misses != 3 {
		t.Fatalf("hits/misses = %d/%d, want 7/3", got.Hits, got.Misses)
	}
	if got.SizeBytes != 1048576 {
		t.Fatalf("size = %d, want 1048576", got.SizeBytes)
	}
	if got.HitRate != 70 {
		t.Fatalf("hit rate = %v, want 70", got.HitRate)
	}
	if got.Extra["uv_packages"] != "14" {
		t.Fatalf("extra uv_packages = %q, want 14", got.Extra["uv_packages"])
	}
}

func TestParseMetricsEmpty(t *testing.T) {
	got := ParseMetrics("no metrics here")
	want := domain.CacheStats{}
	if got.Hits != want.Hits || got.Misses != want.Misses || got.SizeBytes != want.SizeBytes || got.HitRate != want.HitRate {
		t.Fatalf("got %+v, want empty stats", got)
	}
}
