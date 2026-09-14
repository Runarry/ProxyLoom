package main

import (
	"encoding/json"
	"errors"
	"github.com/Runarry/ProxyLoom/internal/bootstrap"
	"io"
)

func adminBootstrap(args []string, output io.Writer) error {
	if len(args) != 7 || args[1] != "--directory" || args[3] != "--architecture" {
		return errors.New("admin_invalid_deployment_arguments")
	}
	runnerOnly := args[0] == "init-runner-identity"
	publicURL, epoch := "", ""
	if runnerOnly {
		if args[5] != "--authorization-epoch" {
			return errors.New("admin_invalid_deployment_arguments")
		}
		epoch = args[6]
	} else {
		if args[5] != "--public-url" {
			return errors.New("admin_invalid_deployment_arguments")
		}
		publicURL = args[6]
	}
	info, err := bootstrap.Create(args[2], args[4], publicURL, epoch, runnerOnly)
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(info)
}
