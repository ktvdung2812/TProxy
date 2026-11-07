package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"text/tabwriter"
	"time"
)

const clusterUsage = `Usage:
  tproxy cluster status [--json] [--url URL] [--secret SECRET]
  tproxy cluster create --name NAME --hub-url URL [--allow-insecure] [--no-lend] [--rpm 20] [--streams 2] [--url URL] [--secret SECRET]
  tproxy cluster token [--label LABEL] [--ttl 24h] [--uses 1] [--url URL] [--secret SECRET]
  tproxy cluster join <token> [--name NAME] [--owner OWNER] [--allow-insecure] [--no-lend] [--rpm 20] [--streams 2] [--url URL] [--secret SECRET]
  tproxy cluster leave [--yes] [--url URL] [--secret SECRET]
  tproxy cluster dissolve [--yes] [--url URL] [--secret SECRET]
  tproxy cluster help
`

type clusterStatus struct {
	Role string `json:"role"`
	Node struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"node"`
	Cluster *struct {
		Name   string `json:"name"`
		HubURL string `json:"hub_url"`
	} `json:"cluster"`
	Link *struct {
		State     string `json:"state"`
		RTT       any    `json:"rtt_ms"`
		LastError string `json:"last_error"`
	} `json:"link"`
	Lending struct {
		Enabled bool     `json:"enabled"`
		Models  []string `json:"models"`
		Limits  struct {
			RPM     int `json:"requests_per_minute"`
			Streams int `json:"concurrent_streams"`
		} `json:"limits"`
	} `json:"lending"`
	Borrowing struct {
		Enabled bool   `json:"enabled"`
		Mode    string `json:"mode"`
	} `json:"borrowing"`
	Nodes []struct {
		Name    string `json:"name"`
		Role    string `json:"role"`
		Status  string `json:"status"`
		Online  bool   `json:"online"`
		Self    bool   `json:"self"`
		Version string `json:"version"`
		Offers  []struct {
			EligibleAccounts int `json:"eligible_accounts"`
		} `json:"offers"`
	} `json:"nodes"`
	Warnings []string `json:"warnings"`
}

func maybeRunCluster(args []string) (bool, error) {
	if len(args) == 0 || args[0] != "cluster" {
		return false, nil
	}
	return true, runCluster(args[1:], os.Stdin, os.Stdout, os.Stderr)
}

func runCluster(args []string, stdin io.Reader, stdout, stderr io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "-h" || args[0] == "--help" {
		_, err := io.WriteString(stdout, clusterUsage)
		return err
	}
	command := args[0]
	switch command {
	case "status", "create", "token", "join", "leave", "dissolve":
	default:
		return fmt.Errorf("unknown cluster subcommand %q\n%s", command, clusterUsage)
	}
	fs := flag.NewFlagSet("cluster "+command, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { fmt.Fprint(stderr, clusterUsage) }
	baseURL := fs.String("url", defaultSetupURL(), "tproxy base URL")
	secret := fs.String("secret", os.Getenv("TPROXY_ADMIN_SECRET"), "admin secret")
	var jsonOutput, allowInsecure, noLend, yes *bool
	var name, hubURL, owner, label *string
	var rpm, streams, uses *int
	var ttl *time.Duration
	switch command {
	case "status":
		jsonOutput = fs.Bool("json", false, "print JSON")
	case "create", "join":
		name = fs.String("name", "", "name")
		allowInsecure = fs.Bool("allow-insecure", false, "allow insecure hub URL")
		noLend = fs.Bool("no-lend", false, "disable lending")
		rpm = fs.Int("rpm", 20, "requests per minute")
		streams = fs.Int("streams", 2, "concurrent streams")
		if command == "create" {
			hubURL = fs.String("hub-url", "", "hub URL")
		} else {
			owner = fs.String("owner", "", "node owner")
		}
	case "token":
		label = fs.String("label", "", "token label")
		ttl = fs.Duration("ttl", 24*time.Hour, "token lifetime")
		uses = fs.Int("uses", 1, "maximum uses")
	case "leave", "dissolve":
		yes = fs.Bool("yes", false, "skip confirmation")
	}
	// The standard flag parser stops at the first positional argument.
	flagArgs := args[1:]
	var leadingToken string
	if command == "join" && len(flagArgs) > 0 && !strings.HasPrefix(flagArgs[0], "-") {
		leadingToken = flagArgs[0]
		flagArgs = flagArgs[1:]
	}
	if err := fs.Parse(flagArgs); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	positional := fs.Args()
	if leadingToken != "" {
		positional = append([]string{leadingToken}, positional...)
	}
	if command == "join" {
		if len(positional) != 1 || positional[0] == "" {
			return fmt.Errorf("join requires one token")
		}
	} else if len(positional) != 0 {
		return fmt.Errorf("unexpected arguments: %s", strings.Join(positional, " "))
	}
	if command == "create" && (strings.TrimSpace(*name) == "" || strings.TrimSpace(*hubURL) == "") {
		return fmt.Errorf("create requires --name and --hub-url")
	}
	reader := bufio.NewReader(stdin)
	client := &setupClient{baseURL: strings.TrimRight(strings.TrimSpace(*baseURL), "/"), secret: strings.TrimSpace(*secret), http: &http.Client{Timeout: 20 * time.Second}}
	if client.secret == "" {
		if f, ok := stdin.(*os.File); ok {
			if info, err := f.Stat(); err == nil && info.Mode()&os.ModeCharDevice != 0 {
				fmt.Fprint(stdout, "Admin secret (leave blank if the dashboard has no password): ")
				line, _ := reader.ReadString('\n')
				client.secret = strings.TrimSpace(line)
			}
		}
	}
	const path = "/api/admin/cluster"
	if command == "status" {
		data, err := client.do(http.MethodGet, path, nil)
		if err != nil {
			return err
		}
		if *jsonOutput {
			var formatted bytes.Buffer
			if err := json.Indent(&formatted, data, "", "  "); err != nil {
				return err
			}
			_, err = fmt.Fprintln(stdout, formatted.String())
			return err
		}
		var status clusterStatus
		if err := json.Unmarshal(data, &status); err != nil {
			return err
		}
		printClusterStatus(stdout, status)
		return nil
	}
	var body any
	switch command {
	case "create", "join":
		lending := map[string]any{"enabled": !*noLend, "models": []string{}, "limits": map[string]int{"requests_per_minute": *rpm, "concurrent_streams": *streams}}
		if command == "create" {
			body = map[string]any{"name": *name, "hub_url": *hubURL, "allow_insecure": *allowInsecure, "lending": lending}
		} else {
			body = map[string]any{"token": positional[0], "node_name": *name, "owner": *owner, "allow_insecure": *allowInsecure, "lending": lending}
		}
	case "token":
		hours := int64(*ttl / time.Hour)
		if *ttl%time.Hour != 0 {
			hours++
		}
		if hours < 1 {
			hours = 1
		}
		body = map[string]any{"label": *label, "ttl_hours": hours, "max_uses": *uses}
	case "leave", "dissolve":
		data, err := client.do(http.MethodGet, path, nil)
		if err != nil {
			return err
		}
		var status clusterStatus
		if err := json.Unmarshal(data, &status); err != nil {
			return err
		}
		if status.Role == "standalone" {
			fmt.Fprintln(stdout, "not in a cluster")
			return nil
		}
		if command == "leave" && status.Role == "hub" {
			return fmt.Errorf("this node is the hub; use tproxy cluster dissolve")
		}
		if command == "dissolve" && status.Role == "member" {
			return fmt.Errorf("this node is a member; use tproxy cluster leave")
		}
		clusterName := ""
		if status.Cluster != nil {
			clusterName = status.Cluster.Name
		}
		if !*yes {
			if command == "leave" {
				fmt.Fprintf(stdout, "Leave the cluster %s? [y/N] ", clusterName)
			} else {
				fmt.Fprintf(stdout, "Dissolve the cluster %s? This removes all member nodes, join tokens and the ledger. [y/N] ", clusterName)
			}
			line, _ := reader.ReadString('\n')
			if !strings.EqualFold(strings.TrimSpace(line), "y") && !strings.EqualFold(strings.TrimSpace(line), "yes") {
				fmt.Fprintln(stdout, "Aborted.")
				return nil
			}
		}
	}
	endpoint := command
	if command == "token" {
		endpoint = "tokens"
	}
	data, err := client.do(http.MethodPost, path+"/"+endpoint, body)
	if err != nil {
		return err
	}
	if command == "token" {
		var result struct {
			Token     string `json:"token"`
			Command   string `json:"command"`
			ExpiresAt string `json:"expires_at"`
		}
		if err := json.Unmarshal(data, &result); err != nil {
			return err
		}
		fmt.Fprintf(stdout, "Token: %s\nCommand: %s\nExpires: %s\nWarning: this token is shown only once.\n", result.Token, result.Command, result.ExpiresAt)
		return nil
	}
	var status clusterStatus
	if err := json.Unmarshal(data, &status); err != nil {
		return err
	}
	clusterName, url, link := "", "", ""
	if status.Cluster != nil {
		clusterName, url = status.Cluster.Name, status.Cluster.HubURL
	}
	if status.Link != nil {
		link = status.Link.State
	}
	switch command {
	case "create":
		fmt.Fprintf(stdout, "Cluster: %s\nHub URL: %s\nRun tproxy cluster token to create a join token.\n", clusterName, url)
	case "join":
		fmt.Fprintf(stdout, "Role: %s\nCluster: %s\nHub URL: %s\nLink: %s\n", status.Role, clusterName, url, link)
	case "leave":
		fmt.Fprintln(stdout, "Left the cluster.")
	case "dissolve":
		fmt.Fprintln(stdout, "Cluster dissolved.")
	}
	return nil
}

func printClusterStatus(w io.Writer, s clusterStatus) {
	fmt.Fprintf(w, "Role: %s\nNode: %s (%s)\n", s.Role, s.Node.Name, s.Node.ID)
	if s.Cluster != nil {
		fmt.Fprintf(w, "Cluster: %s\nHub URL: %s\n", s.Cluster.Name, s.Cluster.HubURL)
	}
	if s.Role == "member" && s.Link != nil {
		fmt.Fprintf(w, "Link: %s", s.Link.State)
		if s.Link.RTT != nil {
			fmt.Fprintf(w, ", RTT: %v ms", s.Link.RTT)
		}
		if s.Link.LastError != "" {
			fmt.Fprintf(w, ", last error: %s", s.Link.LastError)
		}
		fmt.Fprintln(w)
	}
	onOff := func(b bool) string {
		if b {
			return "on"
		}
		return "off"
	}
	fmt.Fprintf(w, "Lending: %s, RPM: %d, streams: %d, models: %s\nBorrowing: %s, mode: %s\n", onOff(s.Lending.Enabled), s.Lending.Limits.RPM, s.Lending.Limits.Streams, strings.Join(s.Lending.Models, ", "), onOff(s.Borrowing.Enabled), s.Borrowing.Mode)
	fmt.Fprintln(w, "Nodes:")
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tROLE\tSTATUS\tONLINE\tOFFERS\tVERSION")
	for _, n := range s.Nodes {
		name := n.Name
		if n.Self {
			name += " (self)"
		}
		online := "offline"
		if n.Online {
			online = "online"
		}
		healthy := 0
		for _, offer := range n.Offers {
			if offer.EligibleAccounts > 0 {
				healthy++
			}
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%d/%d\t%s\n", name, n.Role, n.Status, online, healthy, len(n.Offers), n.Version)
	}
	tw.Flush()
	for _, warning := range s.Warnings {
		fmt.Fprintf(w, "Warning: %s\n", warning)
	}
}
