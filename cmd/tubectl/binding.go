package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"

	"code.cfops.it/sys/tubular/internal"
	"inet.af/netaddr"
)

func bind(e *env, args ...string) error {
	set := e.newFlagSet("bind", "label", "protocol", "ip[/mask]", "port")
	set.Description = "Bind a given prefix, port and protocol to a label."
	if err := set.Parse(args); err != nil {
		return err
	}

	bind, err := bindingFromArgs(set.Args())
	if err != nil {
		return err
	}

	dp, err := e.openDispatcher(false)
	if err != nil {
		return err
	}
	defer dp.Close()

	return dp.AddBinding(bind)
}

func unbind(e *env, args ...string) error {
	set := e.newFlagSet("unbind", "label", "protocol", "ip[/mask]", "port")
	set.Description = "Remove a previously created binding."
	if err := set.Parse(args); err != nil {
		return err
	}

	bind, err := bindingFromArgs(set.Args())
	if err != nil {
		return err
	}

	dp, err := e.openDispatcher(false)
	if err != nil {
		return err
	}
	defer dp.Close()

	if err := dp.RemoveBinding(bind); err != nil {
		return err
	}

	e.stdout.Log("Removed", bind)
	return nil
}

func bindingFromArgs(args []string) (*internal.Binding, error) {
	if n := len(args); n != 4 {
		return nil, fmt.Errorf("expected label, protocol, ip/prefix and port but got %d arguments", n)
	}

	var proto internal.Protocol
	switch args[1] {
	case "udp":
		proto = internal.UDP
	case "tcp":
		proto = internal.TCP
	default:
		return nil, fmt.Errorf("expected proto udp or tcp, got: %s", args[1])
	}

	port, err := strconv.ParseUint(args[3], 10, 16)
	if err != nil {
		return nil, fmt.Errorf("invalid port: %s", err)
	}

	return internal.NewBinding(args[0], proto, args[2], uint16(port))
}

func loadBindings(e *env, args ...string) error {
	type bindingJSON struct {
		Label  string           `json:"label"`
		Prefix netaddr.IPPrefix `json:"prefix"`
	}

	type configJSON struct {
		Bindings []bindingJSON `json:"bindings"`
	}

	set := newFlagSet(e.stderr, "load-bindings", "file")
	set.Description = func() {
		example := configJSON{
			Bindings: []bindingJSON{
				{"foo", netaddr.MustParseIPPrefix("127.0.0.1/32")},
			},
		}

		out, _ := json.MarshalIndent(example, "    ", "    ")

		set.Printf(
			`Load a set of bindings from a JSON formatted file and replace
			the currently active bindings with the ones from the file.

			The format is:

			    %s`,
			string(out),
		)
	}

	if err := set.Parse(args); err != nil {
		return err
	}

	if set.NArg() != 1 {
		set.Usage()
		return errBadArg
	}

	file, err := os.Open(set.Arg(0))
	if err != nil {
		return err
	}
	defer file.Close()

	var config configJSON
	decoder := json.NewDecoder(file)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return fmt.Errorf("%s: %s", file.Name(), err)
	}

	var bindings []*internal.Binding
	for _, bind := range config.Bindings {
		bindings = append(bindings,
			&internal.Binding{
				Label:    bind.Label,
				Prefix:   bind.Prefix.Masked(),
				Protocol: internal.TCP,
				Port:     0,
			},
			&internal.Binding{
				Label:    bind.Label,
				Prefix:   bind.Prefix.Masked(),
				Protocol: internal.UDP,
				Port:     0,
			},
		)
	}

	dp, err := e.openDispatcher(false)
	if err != nil {
		return err
	}
	defer dp.Close()

	_, err = dp.ReplaceBindings(bindings)
	return err
}
