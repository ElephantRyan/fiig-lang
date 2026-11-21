package servecmd

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"fiig-lang/compiler/adapter"
	"fiig-lang/tooling/migrate"
	"fiig-lang/tooling/runprogram"
)

// ADR 0132: `fiig serve .` runs what the program's application declaration
// says, a role when one is named, and refuses a program that declares none.

func appWithApplication(application string) map[string]string {
	files := map[string]string{}
	for k, v := range startupApp {
		files[k] = v
	}
	if application != "" {
		files["app/main.fiig"] = application
	}
	return files
}

// serving loads the program with the arguments, and says whether its HTTP
// adapter answers and whether a startup loop is among the loops.
func serving(t *testing.T, application string, args ...string) (http, startup bool, err error) {
	t.Helper()
	t.Setenv("STORAGE_READY", "true")
	root := t.TempDir()
	writeTree(t, root, appWithApplication(application))
	env, _ := runprogram.NewEnvironment("")
	handler, loops, _, caps, err := load(filepath.Join(root, "app"), args, "", nil, nil, nil, adapter.DefaultLimits(), env, nil, migrate.Apply)
	if err != nil {
		return false, false, err
	}
	t.Cleanup(caps.Close)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/hello", nil))
	for _, l := range loops {
		startup = startup || l.startup
	}
	return rec.Code == 200, startup, nil
}

const oneProcess = "application Main {\n    serve {\n        api\n        preflight\n    }\n}\n"
const twoProcesses = "application Main {\n    serve web {\n        api\n    }\n    serve worker {\n        preflight\n    }\n}\n"

func TestServeRunsTheApplicationsOnlyBlock(t *testing.T) {
	if http, startup, err := serving(t, oneProcess); err != nil || !http || !startup {
		t.Errorf("both adapters: http=%v startup=%v err=%v", http, startup, err)
	}
}

func TestARoleIsOneProcess(t *testing.T) {
	if http, startup, err := serving(t, twoProcesses, "web"); err != nil || !http || startup {
		t.Errorf("web: http=%v startup=%v err=%v", http, startup, err)
	}
	if http, startup, err := serving(t, twoProcesses, "worker"); err != nil || http || !startup {
		t.Errorf("worker: http=%v startup=%v err=%v", http, startup, err)
	}
	if _, _, err := serving(t, twoProcesses); err == nil || !strings.Contains(err.Error(), "runs as web or worker") || !strings.Contains(err.Error(), "fiig serve . web") {
		t.Errorf("no role named: err = %v", err)
	}
}

func TestACommandLineNameBeatsTheApplication(t *testing.T) {
	// One that is no role: the adapter itself, whatever the application says.
	if http, startup, err := serving(t, oneProcess, "preflight"); err != nil || http || !startup {
		t.Errorf("only preflight: http=%v startup=%v err=%v", http, startup, err)
	}
	if http, startup, err := serving(t, twoProcesses, "api", "preflight"); err != nil || !http || !startup {
		t.Errorf("two names: http=%v startup=%v err=%v", http, startup, err)
	}
}

func TestAProgramWithNoApplicationIsRefused(t *testing.T) {
	_, _, err := serving(t, "")
	if err == nil || !strings.Contains(err.Error(), "has no application declaration") || !strings.Contains(err.Error(), "application MyApp {") {
		t.Errorf("err = %v, want it to say how to declare one", err)
	}
	// Naming the adapters still works without one.
	if http, _, err := serving(t, "", "api"); err != nil || !http {
		t.Errorf("a named adapter without an application: http=%v err=%v", http, err)
	}
}

func TestAnApplicationNamingNoAdapterIsRefused(t *testing.T) {
	_, _, err := serving(t, "application Main {\n    serve {\n        api\n        nothing\n    }\n}\n")
	if err == nil {
		t.Fatal("an application naming an adapter that isn't there was served")
	}
}
