package browser

import (
	"errors"
	"testing"
)

func TestOpener_Open_LauncherPrecedence(t *testing.T) {
	tests := []struct {
		name         string
		config       string
		env          map[string]string
		wantLauncher string
	}{
		{
			name:         "GPRT_BROWSER wins over config and BROWSER",
			config:       "configured-browser",
			env:          map[string]string{"GPRT_BROWSER": "gprt-browser", "BROWSER": "env-browser"},
			wantLauncher: "gprt-browser",
		},
		{
			name:         "config wins over BROWSER",
			config:       "configured-browser",
			env:          map[string]string{"BROWSER": "env-browser"},
			wantLauncher: "configured-browser",
		},
		{
			name:         "BROWSER used when GPRT_BROWSER and config are empty",
			config:       "",
			env:          map[string]string{"BROWSER": "env-browser"},
			wantLauncher: "env-browser",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var gotName string
			var gotArgs []string
			o := &Opener{
				Config: tc.config,
				Env:    func(key string) string { return tc.env[key] },
				Run: func(name string, args ...string) error {
					gotName = name
					gotArgs = args
					return nil
				},
				Fallback: func(string) error {
					t.Fatal("Fallback called, want Run to be used")
					return nil
				},
			}
			if err := o.Open("https://example.com/pr/1"); err != nil {
				t.Fatalf("Open() error = %v", err)
			}
			if gotName != tc.wantLauncher {
				t.Errorf("Run launcher = %q, want %q", gotName, tc.wantLauncher)
			}
			if len(gotArgs) != 1 || gotArgs[0] != "https://example.com/pr/1" {
				t.Errorf("Run args = %v, want [%q]", gotArgs, "https://example.com/pr/1")
			}
		})
	}
}

func TestOpener_Open_FallbackWhenNoLauncherConfigured(t *testing.T) {
	var gotURL string
	o := &Opener{
		Env: func(string) string { return "" },
		Run: func(name string, args ...string) error {
			t.Fatal("Run called, want Fallback to be used")
			return nil
		},
		Fallback: func(url string) error {
			gotURL = url
			return nil
		},
	}
	if err := o.Open("https://example.com"); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if gotURL != "https://example.com" {
		t.Errorf("Fallback url = %q, want %q", gotURL, "https://example.com")
	}
}

func TestOpener_Open_RejectsUnsupportedURLs(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{"file scheme", "file:///etc/passwd"},
		{"javascript scheme", "javascript:alert(1)"},
		{"empty url", ""},
		{"missing host", "https:///path"},
		{"unparsable url", "::not a url::"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			o := &Opener{
				Env: func(string) string { return "" },
				Run: func(name string, args ...string) error {
					t.Fatal("Run called, want rejection before resolving a launcher")
					return nil
				},
				Fallback: func(string) error {
					t.Fatal("Fallback called, want rejection before falling back")
					return nil
				},
			}
			if err := o.Open(tc.url); err == nil {
				t.Errorf("Open(%q) error = nil, want error", tc.url)
			}
		})
	}
}

func TestOpener_Open_SplitsLauncherArgs(t *testing.T) {
	var gotName string
	var gotArgs []string
	o := &Opener{
		Config: "flatpak run org.mozilla.firefox",
		Env:    func(string) string { return "" },
		Run: func(name string, args ...string) error {
			gotName = name
			gotArgs = args
			return nil
		},
		Fallback: func(string) error { return nil },
	}
	if err := o.Open("https://example.com"); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if gotName != "flatpak" {
		t.Errorf("Run name = %q, want %q", gotName, "flatpak")
	}
	wantArgs := []string{"run", "org.mozilla.firefox", "https://example.com"}
	if len(gotArgs) != len(wantArgs) {
		t.Fatalf("Run args = %v, want %v", gotArgs, wantArgs)
	}
	for i := range wantArgs {
		if gotArgs[i] != wantArgs[i] {
			t.Errorf("Run args[%d] = %q, want %q", i, gotArgs[i], wantArgs[i])
		}
	}
}

func TestOpener_Open_RunErrorPropagated(t *testing.T) {
	wantErr := errors.New("boom")
	o := &Opener{
		Config:   "some-browser",
		Env:      func(string) string { return "" },
		Run:      func(name string, args ...string) error { return wantErr },
		Fallback: func(string) error { return nil },
	}
	if err := o.Open("https://example.com"); !errors.Is(err, wantErr) {
		t.Errorf("Open() error = %v, want it to wrap %v", err, wantErr)
	}
}

func TestOpener_Open_WhitespaceOnlyLauncherFallsBackWithoutPanicking(t *testing.T) {
	var gotURL string
	o := &Opener{
		Config: " ",
		Env: func(key string) string {
			if key == "BROWSER" {
				return "\t\n"
			}
			return ""
		},
		Run: func(name string, args ...string) error {
			t.Fatal("Run called, want Fallback to be used")
			return nil
		},
		Fallback: func(url string) error {
			gotURL = url
			return nil
		},
	}
	if err := o.Open("https://example.com"); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if gotURL != "https://example.com" {
		t.Errorf("Fallback url = %q, want %q", gotURL, "https://example.com")
	}
}

func TestOpener_Open_SplitsLauncherWithQuotedPaths(t *testing.T) {
	var gotName string
	var gotArgs []string
	o := &Opener{
		Config: `"/Applications/My Browser.app/Contents/MacOS/browser" --new-window`,
		Env:    func(string) string { return "" },
		Run: func(name string, args ...string) error {
			gotName = name
			gotArgs = args
			return nil
		},
		Fallback: func(string) error { return nil },
	}
	if err := o.Open("https://example.com"); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	wantName := "/Applications/My Browser.app/Contents/MacOS/browser"
	if gotName != wantName {
		t.Errorf("Run name = %q, want %q", gotName, wantName)
	}
	wantArgs := []string{"--new-window", "https://example.com"}
	if len(gotArgs) != len(wantArgs) {
		t.Fatalf("Run args = %v, want %v", gotArgs, wantArgs)
	}
	for i := range wantArgs {
		if gotArgs[i] != wantArgs[i] {
			t.Errorf("Run args[%d] = %q, want %q", i, gotArgs[i], wantArgs[i])
		}
	}
}

func TestOpener_Open_UnparsableLauncherReturnsError(t *testing.T) {
	o := &Opener{
		Config: `unterminated "quote`,
		Env:    func(string) string { return "" },
		Run: func(name string, args ...string) error {
			t.Fatal("Run called, want the parse error to be returned first")
			return nil
		},
		Fallback: func(string) error {
			t.Fatal("Fallback called, want the parse error to be returned first")
			return nil
		},
	}
	if err := o.Open("https://example.com"); err == nil {
		t.Error("Open() error = nil, want a launcher parse error")
	}
}

func TestNew_WiresDefaults(t *testing.T) {
	o := New("my-browser", nil)
	if o.Config != "my-browser" {
		t.Errorf("Config = %q, want %q", o.Config, "my-browser")
	}
	if o.Env == nil {
		t.Error("Env is nil, want a default resolver")
	}
	if o.Run == nil {
		t.Error("Run is nil, want a default runner")
	}
	if o.Fallback == nil {
		t.Error("Fallback is nil, want a default fallback")
	}
	if o.OnExit != nil {
		t.Error("OnExit is non-nil for a nil onExit argument, want it to stay nil (discard silently)")
	}
}

func TestNew_WiresOnExit(t *testing.T) {
	called := make(chan struct{}, 1)
	o := New("", func(error) { called <- struct{}{} })
	if o.OnExit == nil {
		t.Fatal("OnExit is nil, want the callback passed to New")
	}
	o.OnExit(nil)
	select {
	case <-called:
	default:
		t.Error("OnExit did not invoke the callback passed to New")
	}
}
