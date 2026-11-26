// Copyright 2026 The pg-regression-radar Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strconv"
	"strings"
	"time"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/joao00001/pg-regression-radar/internal/dashboard"
	"github.com/joao00001/pg-regression-radar/internal/dashboard/cluster"
	"github.com/joao00001/pg-regression-radar/internal/dashboard/layout"
	"github.com/joao00001/pg-regression-radar/internal/httpserver"
	"github.com/joao00001/pg-regression-radar/internal/observe"
)

const dashboardUsage = `Usage: pg-regression-radar dashboard [flags]

Opens the live dashboard in your browser, served from this machine only.

It reads the manager of the cluster your kubeconfig points at, through a
port-forward made with your own credentials (the same access "kubectl
port-forward" needs: get on leases and pods, create on pods/portforward).
Nothing is installed in the cluster and nothing in it is changed. The one thing
the dashboard writes is your own edits to the boards, in --data-dir.

Subcommands:
  config validate FILE   check a dashboard file and say what is wrong, by line
  config dump            print the default dashboard file, to start from

Flags:
`

// dashboardOptions is everything the dashboard command is told.
type dashboardOptions struct {
	fixture        string
	port           int
	listen         string
	insecureListen bool
	open           bool
	showQueries    bool
	kubeconfig     string
	kubeContext    string
	namespace      string
	lease          string
	remotePort     int
	url            string
	config         string
	dataDir        string
	noSave         bool
}

// RunDashboard runs the dashboard command.
func RunDashboard(args []string) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return runDashboard(ctx, args, os.Stdout, os.Stderr)
}

func parseDashboardFlags(args []string, stderr io.Writer) (dashboardOptions, int, bool) {
	var o dashboardOptions
	fs := flag.NewFlagSet("dashboard", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { _, _ = fmt.Fprint(stderr, dashboardUsage); fs.PrintDefaults() }
	fs.StringVar(&o.fixture, "fixture", "", "serve recorded data from this JSON file instead of a cluster (for trying the dashboard without one)")
	fs.IntVar(&o.port, "port", 8765, "port to listen on")
	fs.StringVar(&o.listen, "listen", "127.0.0.1", "address to listen on; only a loopback address is accepted unless --insecure-listen is given")
	fs.BoolVar(&o.insecureListen, "insecure-listen", false, "allow --listen to be an address other people can reach; the dashboard has no sign-in, so anyone who can reach it sees your deploys")
	fs.BoolVar(&o.open, "open", false, "open the dashboard in the default browser")
	fs.BoolVar(&o.showQueries, "show-queries", false, "show the normalised SQL text of queries (the manager must allow it too, with --observe-allow-queries)")
	fs.StringVar(&o.kubeconfig, "kubeconfig", "", "kubeconfig file (default: $KUBECONFIG or ~/.kube/config)")
	fs.StringVar(&o.kubeContext, "context", "", "kubeconfig context (default: the current one)")
	fs.StringVar(&o.namespace, "namespace", "", "namespace the manager runs in (default: look for it in every namespace you may list leases in)")
	fs.StringVar(&o.lease, "lease-name", cluster.DefaultLease, "name of the manager's leader-election lease")
	fs.IntVar(&o.remotePort, "remote-port", 8083, "the manager's --observe-addr port")
	fs.StringVar(&o.config, "config", "", "dashboard file (YAML) describing the boards and panels; check one with `dashboard config validate FILE`, start from `dashboard config dump`")
	fs.StringVar(&o.dataDir, "data-dir", "", "directory where the dashboard keeps your edits between runs (panel settings, queries, positions, time ranges) (default: pg-regression-radar/dashboard in your user configuration directory); it can be a mounted volume")
	fs.BoolVar(&o.noSave, "no-save", false, "keep your edits in the browser only; write nothing to disk")
	fs.StringVar(&o.url, "url", "", "read the Observe API at this URL instead of forwarding to the cluster (for an API you exposed yourself)")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return o, 0, false
		}
		return o, 2, false
	}
	if o.port < 1 || o.port > 65535 {
		_, _ = fmt.Fprintf(stderr, "dashboard: --port %d is not a valid port\n", o.port)
		return o, 2, false
	}
	if o.remotePort < 1 || o.remotePort > 65535 {
		_, _ = fmt.Fprintf(stderr, "dashboard: --remote-port %d is not a valid port\n", o.remotePort)
		return o, 2, false
	}
	if err := checkListen(o.listen, o.insecureListen); err != nil {
		_, _ = fmt.Fprintf(stderr, "dashboard: %v\n", err)
		return o, 2, false
	}
	if o.fixture != "" && o.url != "" {
		_, _ = fmt.Fprintln(stderr, "dashboard: --fixture and --url cannot be used together")
		return o, 2, false
	}
	return o, 0, true
}

// checkListen accepts a loopback address, or anything else only when the person
// said --insecure-listen. A name is resolved by what it is, not by how it looks.
func checkListen(host string, insecure bool) error {
	ip := net.ParseIP(host)
	if ip == nil {
		if host == "localhost" {
			return nil
		}
		if insecure {
			return nil
		}
		return fmt.Errorf("--listen %q is not an IP address; use 127.0.0.1, ::1 or localhost (or --insecure-listen to expose the dashboard on purpose)", host)
	}
	if ip.IsLoopback() || insecure {
		return nil
	}
	return fmt.Errorf("--listen %s would let other machines read your deploys, and the dashboard has no sign-in; use a loopback address, or add --insecure-listen if you mean it", host)
}

func runDashboard(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	if len(args) > 0 && args[0] == "config" {
		return runDashboardConfig(args[1:], stdout, stderr)
	}
	o, code, ok := parseDashboardFlags(args, stderr)
	if !ok {
		return code
	}
	var layoutFn dashboard.LayoutProvider
	if o.config != "" {
		layoutFn = dashboard.FileLayout(o.config)
		// Say so now, with line numbers, rather than serving a page of errors.
		if _, ps := layoutFn(); len(ps) > 0 {
			printProblems(stderr, o.config, ps)
			return 1
		}
	}

	var (
		backend http.Handler
		status  func() any
		cfg     dashboard.Config
		run     func(context.Context) // keeps the connection alive, if there is one
		banner  string
	)
	switch {
	case o.fixture != "":
		f, err := observe.LoadFixture(o.fixture)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "dashboard: %v\n", err)
			return 1
		}
		// The recorded data lives at its own moment; give the API that moment as its
		// clock, moving forward, so "10 minutes ago" and "now-6h" mean what they did then.
		base, started := f.Overview.GeneratedAt, time.Now()
		clockNow := func() time.Time {
			if base.IsZero() {
				return time.Now()
			}
			return base.Add(time.Since(started))
		}
		mux := http.NewServeMux()
		(&observe.Handler{
			Source:       observe.Scoped(observe.NewFixtureSource(f), observe.Scope{All: true}),
			AllowQueries: o.showQueries,
			Now:          clockNow,
		}).Routes(mux)
		backend = mux
		cfg = dashboard.Config{
			Origin:      "fixture",
			Note:        "Recorded by the engine over a simulated workload (" + o.fixture + "). Not a live cluster.",
			ShowQueries: o.showQueries,
		}
		banner = "recorded data from " + o.fixture
	default:
		sup, label, err := newSupervisor(o)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "dashboard: %v\n", err)
			return 1
		}
		backend, run = sup.Handler(), sup.Run
		status = func() any { return sup.State() }
		cfg = dashboard.Config{Origin: "cluster", Note: label, ShowQueries: o.showQueries}
		banner = label
	}

	ln, err := net.Listen("tcp", net.JoinHostPort(o.listen, strconv.Itoa(o.port)))
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "dashboard: cannot listen on %s:%d: %v (pick another with --port)\n", o.listen, o.port, err)
		return 1
	}
	profile, perr := openProfile(o)
	if perr != nil {
		_, _ = fmt.Fprintf(stderr, "dashboard: your edits will be kept in the browser only: %v\n", perr)
	}
	srv := &dashboard.Server{
		Backend: backend,
		Profile: profile,
		Port:    strconv.Itoa(ln.Addr().(*net.TCPAddr).Port),
		Config:  cfg,
		Status:  status,
		Layout:  layoutFn,
		AnyHost: o.insecureListen,
	}
	hs := httpserver.New(ln.Addr().String(), srv.Handler())
	hs.WriteTimeout = 0 // the event stream lasts; each response clears it too
	shown := o.listen
	if shown == "::1" {
		shown = "[::1]"
	}
	link := fmt.Sprintf("http://%s:%s/", shown, srv.Port)
	if o.insecureListen {
		_, _ = fmt.Fprintf(stderr, "dashboard: WARNING listening on %s with no sign-in; anyone who can reach it can read your deploys\n", o.listen)
	}
	_, _ = fmt.Fprintf(stdout, "Dashboard on %s (%s). Press Ctrl+C to stop.\n", link, banner)
	if profile != nil {
		_, _ = fmt.Fprintf(stdout, "Your edits are saved in %s\n", profile.Where())
	}
	if o.open {
		if err := openBrowser(link); err != nil {
			_, _ = fmt.Fprintf(stderr, "dashboard: could not open a browser (%v); open %s yourself\n", err, link)
		}
	}

	if run != nil {
		go run(ctx)
	}
	errc := make(chan error, 1)
	go func() { errc <- hs.Serve(ln) }()
	select {
	case <-ctx.Done():
		sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = hs.Shutdown(sctx)
		return 0
	case err := <-errc:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			_, _ = fmt.Fprintf(stderr, "dashboard: %v\n", err)
			return 1
		}
		return 0
	}
}

// openProfile prepares the file the person's edits are kept in, or nothing when
// saving is off or the directory cannot be written (the page then keeps them in
// the browser, and the command says so).
func openProfile(o dashboardOptions) (*dashboard.Profile, error) {
	if o.noSave {
		return nil, nil
	}
	if o.insecureListen {
		// With no sign-in, whoever can reach the page could also overwrite the file.
		return nil, errors.New("saving is off with --insecure-listen: the page has no sign-in, so anyone who can reach it could overwrite your edits")
	}
	dir := o.dataDir
	if dir == "" {
		d, err := dashboard.DefaultDataDir()
		if err != nil {
			return nil, err
		}
		dir = d
	}
	return dashboard.OpenProfile(dir, dashboard.ProfileKey(o.config))
}

// newSupervisor builds what reaches the manager: a fixed URL when --url is
// given, otherwise a port-forward to the leader found through the kubeconfig.
func newSupervisor(o dashboardOptions) (*cluster.Supervisor, string, error) {
	verify := func(ctx context.Context, hostport string) error {
		return cluster.CheckVersion(ctx, hostport, []string{observe.Version})
	}
	if o.url != "" {
		u, err := url.Parse(o.url)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return nil, "", fmt.Errorf("--url %q is not an http(s) URL like http://127.0.0.1:8083", o.url)
		}
		u.Path, u.RawQuery, u.Fragment = "", "", ""
		return &cluster.Supervisor{Static: u}, "Reading the Observe API at " + u.Host + ".", nil
	}
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	rules.ExplicitPath = o.kubeconfig
	cc := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{CurrentContext: o.kubeContext})
	restCfg, err := cc.ClientConfig()
	if err != nil {
		return nil, "", fmt.Errorf("reading the kubeconfig: %w (use --kubeconfig/--context, or --url to skip the cluster)", err)
	}
	raw, _ := cc.RawConfig()
	name := o.kubeContext
	if name == "" {
		name = raw.CurrentContext
	}
	kube, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		return nil, "", fmt.Errorf("preparing the Kubernetes client: %w", err)
	}
	sup := &cluster.Supervisor{
		Find:   cluster.LeaderFinder(kube, o.namespace, o.lease, o.remotePort, time.Now),
		Dial:   &cluster.KubeDialer{Config: restCfg, Kube: kube},
		Verify: verify,
	}
	label := "Reading the manager through a port-forward"
	if name != "" {
		label += " (context " + name + ")"
	}
	return sup, label + ".", nil
}

// openBrowser asks the operating system to open url. The URL is built here from
// the listen address and port; nothing from a request or a file reaches the command.
func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url) // #nosec G204 -- fixed program, URL built from a validated address
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url) // #nosec G204 -- as above
	default:
		cmd = exec.Command("xdg-open", url) // #nosec G204 -- as above
	}
	return cmd.Start()
}

const dashboardConfigUsage = `Usage:
  pg-regression-radar dashboard config validate FILE
  pg-regression-radar dashboard config dump [--catalog]

validate checks a dashboard file the way "dashboard --config" will read it, and
prints every problem with its line and column. It exits 0 when the file is fine.

dump prints the default dashboard as a file you can edit and pass to --config.
With --catalog it lists the panel types instead: their sizes and options.
`

func runDashboardConfig(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprint(stderr, dashboardConfigUsage)
		return 2
	}
	switch args[0] {
	case "validate":
		if len(args) != 2 {
			_, _ = fmt.Fprint(stderr, dashboardConfigUsage)
			return 2
		}
		_, ps := dashboard.FileLayout(args[1])()
		if len(ps) > 0 {
			printProblems(stderr, args[1], ps)
			return 1
		}
		_, _ = fmt.Fprintf(stdout, "%s: ok\n", args[1])
		return 0
	case "dump":
		fs := flag.NewFlagSet("dashboard config dump", flag.ContinueOnError)
		fs.SetOutput(stderr)
		catalog := fs.Bool("catalog", false, "list the panel types instead")
		if err := fs.Parse(args[1:]); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				return 0
			}
			return 2
		}
		if *catalog {
			printCatalog(stdout)
			return 0
		}
		out, err := layout.Marshal(layout.Default())
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "dashboard: %v\n", err)
			return 1
		}
		_, _ = stdout.Write(out)
		return 0
	}
	_, _ = fmt.Fprintf(stderr, "dashboard config: unknown command %q\n\n%s", args[0], dashboardConfigUsage)
	return 2
}

func printProblems(w io.Writer, file string, ps []layout.Problem) {
	for _, p := range ps {
		if p.Line > 0 {
			_, _ = fmt.Fprintf(w, "%s:%d:%d: %s\n", file, p.Line, p.Col, p.Msg)
		} else {
			_, _ = fmt.Fprintf(w, "%s: %s\n", file, p.Msg)
		}
	}
}

func printCatalog(w io.Writer) {
	for _, pt := range layout.Catalog() {
		_, _ = fmt.Fprintf(w, "%s\n  %s\n  size: at least %d x %d cells, %d x %d when added (the grid is %d wide)\n", pt.Type, pt.Doc, pt.MinW, pt.MinH, pt.W, pt.H, layout.Columns)
		if len(pt.Query) > 0 {
			_, _ = fmt.Fprintf(w, "  query fields: %s\n", strings.Join(pt.Query, ", "))
		}
		for _, o := range pt.Options {
			switch o.Kind {
			case layout.OptInt:
				_, _ = fmt.Fprintf(w, "  option %s: whole number %d to %d, default %v. %s\n", o.Name, o.Min, o.Max, o.Default, o.Doc+onlyWhen(o))
			case layout.OptEnum:
				_, _ = fmt.Fprintf(w, "  option %s: one of %s, default %v. %s\n", o.Name, strings.Join(o.Values, ", "), o.Default, o.Doc+onlyWhen(o))
			default:
				_, _ = fmt.Fprintf(w, "  option %s: true or false, default %v. %s\n", o.Name, o.Default, o.Doc+onlyWhen(o))
			}
		}
	}
}

// onlyWhen says, for an option that applies to one visualization only, which.
func onlyWhen(o layout.Option) string {
	if o.When == "" {
		return ""
	}
	return " Applies only when " + strings.Replace(o.When, "=", " is ", 1) + "."
}
