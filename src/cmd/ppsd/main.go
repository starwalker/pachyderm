package main

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/fsouza/go-dockerclient"

	"go.pedge.io/env"
	"go.pedge.io/proto/server"

	"github.com/pachyderm/pachyderm"
	"github.com/pachyderm/pachyderm/src/pfs"
	"github.com/pachyderm/pachyderm/src/pkg/container"
	"github.com/pachyderm/pachyderm/src/pps"
	"github.com/pachyderm/pachyderm/src/pps/jobserver"
	"github.com/pachyderm/pachyderm/src/pps/persist"
	persistserver "github.com/pachyderm/pachyderm/src/pps/persist/server"
	"github.com/pachyderm/pachyderm/src/pps/pipelineserver"
	"google.golang.org/grpc"
)

var (
	defaultEnv = map[string]string{
		"PPS_ADDRESS":       "0.0.0.0",
		"PPS_PORT":          "651",
		"PPS_TRACE_PORT":    "1051",
		"PPS_DATABASE_NAME": "pachyderm",
	}
)

type appEnv struct {
	PachydermPfsd1Port string `env:"PACHYDERM_PFSD_1_PORT"`
	PfsAddress         string `env:"PFS_ADDRESS"`
	Address            string `env:"PPS_ADDRESS"`
	Port               int    `env:"PPS_PORT"`
	DatabaseAddress    string `env:"PPS_DATABASE_ADDRESS"`
	DatabaseName       string `env:"PPS_DATABASE_NAME"`
	DebugPort          int    `env:"PPS_TRACE_PORT"`
}

func main() {
	env.Main(do, &appEnv{}, defaultEnv)
}

func do(appEnvObj interface{}) error {
	appEnv := appEnvObj.(*appEnv)
	containerClient, err := getContainerClient()
	if err != nil {
		return err
	}
	rethinkAPIClient, err := getRethinkAPIClient(appEnv.DatabaseAddress, appEnv.DatabaseName)
	if err != nil {
		return err
	}
	pfsdAddress, err := getPfsdAddress()
	if err != nil {
		return err
	}
	clientConn, err := grpc.Dial(pfsdAddress, grpc.WithInsecure())
	if err != nil {
		return err
	}
	jobAPIServer := jobserver.NewAPIServer(rethinkAPIClient, containerClient)
	jobAPIClient := pps.NewLocalJobAPIClient(jobAPIServer)
	pfsAPIClient := pfs.NewAPIClient(clientConn)
	pipelineAPIServer := pipelineserver.NewAPIServer(pfsAPIClient, jobAPIClient, rethinkAPIClient)
	errC := make(chan error)
	go func() {
		errC <- protoserver.Serve(
			uint16(appEnv.Port),
			func(s *grpc.Server) {
				pps.RegisterJobAPIServer(s, jobAPIServer)
				pps.RegisterPipelineAPIServer(s, pipelineAPIServer)
			},
			protoserver.ServeOptions{
				DebugPort: uint16(appEnv.DebugPort),
				Version:   pachyderm.Version,
			},
		)
	}()
	// TODO(pedge): pretty sure I had this problem before, this is bad, we would
	// prefer a callback for when the server is ready to accept requests
	time.Sleep(1 * time.Second)
	if err := pipelineAPIServer.Start(); err != nil {
		return err
	}
	return <-errC
}

func getContainerClient() (container.Client, error) {
	client, err := docker.NewClientFromEnv()
	if err != nil {
		return nil, err
	}
	return container.NewDockerClient(client), nil
}

func getRethinkAPIClient(address string, databaseName string) (persist.APIClient, error) {
	var err error
	if address == "" {
		address, err = getRethinkAddress()
		if err != nil {
			return nil, err
		}
	}
	if err := persistserver.InitDBs(address, databaseName); err != nil {
		return nil, err
	}
	rethinkAPIServer, err := persistserver.NewRethinkAPIServer(address, databaseName)
	if err != nil {
		return nil, err
	}
	return persist.NewLocalAPIClient(rethinkAPIServer), nil
}

func getRethinkAddress() (string, error) {
	rethinkAddr := os.Getenv("RETHINK_PORT_28015_TCP_ADDR")
	if rethinkAddr == "" {
		return "", errors.New("RETHINK_PORT_28015_TCP_ADDR not set")
	}
	return fmt.Sprintf("%s:28015", rethinkAddr), nil
}

func getPfsdAddress() (string, error) {
	pfsdAddr := os.Getenv("PFSD_PORT_650_TCP_ADDR")
	if pfsdAddr == "" {
		return "", errors.New("PFSD_PORT_650_TCP_ADDR not set")
	}
	return fmt.Sprintf("%s:650", pfsdAddr), nil
}
