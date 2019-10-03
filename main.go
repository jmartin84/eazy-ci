package main

import (
	"context"
	"errors"
	"flag"
	"io/ioutil"
	"log"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/shibbybird/eazy-ci/lib/utils"

	"github.com/shibbybird/eazy-ci/lib/models"
)

var liveContainerIDs = []string{}
var routableLinks = []string{}

// Code for array of environment variables
type arrayFlags []string

func (i *arrayFlags) String() string {
	return "env variables"
}
func (i *arrayFlags) Set(value string) error {
	*i = append(*i, value)
	return nil
}

var envArray arrayFlags

// end of code for environment variables

func main() {
	ctx := context.Background()

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	go func() {
		for range c {
			cleanUp(ctx, 1, nil)
		}
	}()

	filePath := flag.String("f", "./eazy.yml", "The Eazy CI file ")
	flag.Var(&envArray, "e", "Repeat for multiple Environment Variables")
	isDev := flag.Bool("d", false, "Run dependencies and peer depedencies")
	isIntegration := flag.Bool("i", false, "Run dependencies, peer dependencies, and build/start Dockerfile")
	isHostMode := flag.Bool("h", false, "Sets docker to host mode")
	pemKeyPath := flag.String("k", "", "File path for ssh private key for github access")

	flag.Parse()

	fileData, err := ioutil.ReadFile(*filePath)
	if err != nil {
		fail(ctx, err)
	}

	yml, err := models.EazyYmlUnmarshal(fileData)
	if err != nil {
		fail(ctx, err)
	}

	dependencies := []models.EazyYml{}

	err = utils.GetDependencies(yml, &dependencies, *pemKeyPath)

	// try to set up ssh agent if ssh isn't working
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "ssh") {
			err = utils.SetUpSSHKeys()
			if err != nil {
				fail(ctx, err)
			}
			err = utils.GetDependencies(yml, &dependencies, *pemKeyPath)
			if err != nil {
				fail(ctx, err)
			}
		} else {
			fail(ctx, err)
		}
	}

	peerDependencies := []models.EazyYml{}
	peerDependenciesSet := map[string]bool{}

	// Collect Peer Dependencies
	for _, d := range dependencies {
		err = utils.GetPeerDependencies(d, &peerDependencies, peerDependenciesSet, *pemKeyPath)
		if err != nil {
			fail(ctx, errors.New("can not find all peer dependencies"))
		}
	}
	err = utils.GetPeerDependencies(yml, &peerDependencies, peerDependenciesSet, *pemKeyPath)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "ssh") {
			err = utils.SetUpSSHKeys()
			if err != nil {
				fail(ctx, err)
			}
			err = utils.GetPeerDependencies(yml, &peerDependencies, peerDependenciesSet, *pemKeyPath)
			if err != nil {
				fail(ctx, errors.New("can not find peer dependencies on eazy.yml"))
			}
		} else {
			fail(ctx, err)
		}
	}

	for _, d := range peerDependencies {
		startUnit(ctx, d, *isHostMode)
	}

	for _, d := range dependencies {
		startUnit(ctx, d, *isHostMode)
	}

	if len(yml.Integration.Bootstrap) > 0 {
		_, err := utils.BuildAndRunContainer(ctx, yml, models.DockerConfig{
			Env:           envArray,
			Dockerfile:    "Integration.Dockerfile",
			Command:       yml.Integration.Bootstrap,
			Wait:          true,
			IsHostNetwork: *isHostMode,
			ExposePorts:   false,
			Attach:        false,
		}, &routableLinks, &liveContainerIDs)

		if err != nil {
			fail(ctx, err)
		}
	}

	if !*isDev {
		_, err := utils.BuildAndRunContainer(ctx, yml, models.DockerConfig{
			Env:           envArray,
			Dockerfile:    "Dockerfile",
			Command:       []string{},
			Wait:          false,
			IsHostNetwork: *isHostMode,
			ExposePorts:   true,
			Attach:        false,
			IsRootImage:   true,
		}, &routableLinks, &liveContainerIDs)

		if err != nil {
			fail(ctx, err)
		}

		if len(yml.Deployment.Health) > 0 {
			_, err := utils.BuildAndRunContainer(ctx, yml, models.DockerConfig{
				Env:           envArray,
				Dockerfile:    "Integration.Dockerfile",
				Command:       yml.Deployment.Health,
				Wait:          true,
				IsHostNetwork: *isHostMode,
				ExposePorts:   false,
				Attach:        false,
			}, &routableLinks, &liveContainerIDs)

			if err != nil {
				fail(ctx, err)
			}
		}
	}

	if *isDev || *isIntegration {
		_, err := utils.BuildAndRunContainer(ctx, yml, models.DockerConfig{
			Env:           envArray,
			Dockerfile:    "Integration.Dockerfile",
			Command:       []string{"/bin/bash"},
			Wait:          true,
			IsHostNetwork: *isHostMode,
			ExposePorts:   false,
			Attach:        true,
		}, &routableLinks, &liveContainerIDs)

		if err != nil {
			fail(ctx, err)
		}
		/*
			log.Println("You are running in a Development Mode. Use ctrl-c to exit at anytime.")
			go forever()
			select {}
		*/
	} else {
		_, err := utils.BuildAndRunContainer(ctx, yml, models.DockerConfig{
			Env:           envArray,
			Dockerfile:    "Integration.Dockerfile",
			Command:       yml.Integration.RunTest,
			Wait:          true,
			IsHostNetwork: *isHostMode,
			ExposePorts:   false,
			Attach:        false,
		}, &routableLinks, &liveContainerIDs)

		if err != nil {
			fail(ctx, err)
		}
		success(ctx)
	}

	success(ctx)
}

func startUnit(ctx context.Context, yml models.EazyYml, isHostMode bool) {
	if len(yml.Integration.Bootstrap) > 0 {
		// func StartContainerByEazyYml(ctx context.Context, eazy models.EazyYml, commands []string, shouldBlock bool, isHostMode bool, exposePorts bool, imageOverride string) (string, error) {
		_, err := utils.StartContainerByEazyYml(ctx, yml, models.GetLatestIntegrationImageName(yml), models.DockerConfig{
			Command:       yml.Integration.Bootstrap,
			Wait:          true,
			IsHostNetwork: isHostMode,
			ExposePorts:   false,
		}, &routableLinks, &liveContainerIDs)

		if err != nil {
			fail(ctx, err)
		}
	}
	_, err := utils.StartContainerByEazyYml(ctx, yml, "", models.DockerConfig{
		Wait:          false,
		IsHostNetwork: isHostMode,
		ExposePorts:   true,
		IsRootImage:   true,
	}, &routableLinks, &liveContainerIDs)
	if err != nil {
		fail(ctx, err)
	}
	if len(yml.Deployment.Health) > 0 {
		_, err := utils.StartContainerByEazyYml(ctx, yml, models.GetLatestIntegrationImageName(yml), models.DockerConfig{
			Command:       yml.Deployment.Health,
			Wait:          true,
			IsHostNetwork: isHostMode,
			ExposePorts:   false,
		}, &routableLinks, &liveContainerIDs)
		if err != nil {
			fail(ctx, err)
		}
	}
}

func success(ctx context.Context) {
	cleanUp(ctx, 0, nil)
}

func fail(ctx context.Context, err error) {
	cleanUp(ctx, 1, err)
}

func cleanUp(ctx context.Context, exitCode int, err error) {
	log.Println("Do Clean Up!")
	for _, id := range liveContainerIDs {
		err := utils.KillContainer(ctx, id)
		if err != nil {
			log.Println("container already shutdown: " + id)
		}
	}
	log.Println(err)
	os.Exit(exitCode)
}

func forever() {
	for {
		time.Sleep(time.Second)
	}
}
