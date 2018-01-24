package main

import (
	"github.com/juju/loggo"

	"fmt"
	"os"
)

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

func envExists(key string) bool {
	_, ok := os.LookupEnv(key)
	return ok
}

func startup_checks() {
	mandatoryEnvVars := []string{
		"AWS_REGION",
		"JWT_SECRET",
		"DEPLOY_PASSWORD",
	}
	for _, envVar := range mandatoryEnvVars {
		if !envExists(envVar) {
			fmt.Printf("Environment variable missing: %v\n", envVar)
			os.Exit(1)
		}
	}
	// pick up any remaining work
	controller := Controller{}
	err := controller.resume()
	if err != nil {
		fmt.Printf("Couldn't start controller: %v\n", err.Error())
		os.Exit(1)
	}
}

// @title ecs-deploy
// @version 0.0.1
// @description ecs-deploy is the glue between your CI and ECS. It automates deploys based a simple JSON file Edit
// @contact.name Edward Viaene
// @contact.url	https://github.com/in4it/ecs-deploy
// @contact.email	ward@in4it.io
// license.name	Apache 2.0
func main() {
	// set logging to debug
	if getEnv("DEBUG", "") == "true" {
		loggo.ConfigureLoggers(`<root>=DEBUG`)
	}

	// startup checks
	startup_checks()

	// Launch API
	api := API{}
	err := api.launch()
	if err != nil {
		panic(err)
	}
}

// int64 min function
func Min(x, y int64) int64 {
	if x < y {
		return x
	}
	return y
}

// int64 max function
func Max(x, y int64) int64 {
	if x > y {
		return x
	}
	return y
}
