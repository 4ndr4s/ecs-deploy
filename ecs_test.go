package ecsdeploy

import (
	"testing"

	"github.com/in4it/ecs-deploy/util"
)

func TestWaitUntilServicesStable(t *testing.T) {
	if accountId == nil {
		t.Skip(noAWSMsg)
	}
	ecs := ECS{}
	err := ecs.waitUntilServicesStable(util.GetEnv("TEST_CLUSTERNAME", "test-cluster"), util.GetEnv("TEST_SERVICENAME", "ecs-deploy"), 10)
	if err != nil {
		t.Errorf("Error: %v", err)
		return
	}
}
