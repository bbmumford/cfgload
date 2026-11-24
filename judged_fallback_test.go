package cfgload_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/bbmumford/cfgload"
)

// A judged load whose fetch fails is refused: it never falls back to the
// environment, which carries no signature and no header for the Judge, and
// the caller's target is left as it was.
func TestAJudgedLoadDoesNotFallBackToTheEnvironment(t *testing.T) {
	t.Setenv("FALLBACK_TEST_ENDPOINT", "https://env.example")
	t.Setenv("FALLBACK_TEST_TOKEN", "env-token")
	judged := 0
	var target envTarget
	err := cfgload.Load(&target, cfgload.LoadOptions{
		URL:              "file://" + filepath.Join(t.TempDir(), "absent.yaml"),
		AllowEnvFallback: true,
		VerifySignature:  true,
		PublicKey:        "MCowBQYDK2VwAyEA",
		Judge:            func(cfgload.Verified) error { judged++; return nil },
		EnvLookup:        func(v cfgload.EnvVar) (string, bool) { return os.LookupEnv(v.Name) },
	})
	if err == nil {
		t.Fatalf("a judged load whose fetch failed was accepted: %+v", target)
	}
	if target.Endpoint != "" || target.Auth.Token != "" {
		t.Fatalf("the target was filled from the environment: %+v", target)
	}
	if judged != 0 {
		t.Fatalf("the Judge was called %d times on no document", judged)
	}
}
