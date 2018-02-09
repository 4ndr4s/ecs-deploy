package ecsdeploy

import (
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/request"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/in4it/ecs-deploy/util"
	"github.com/juju/loggo"

	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// logging
var ecsLogger = loggo.GetLogger("ecs")

// ECS struct
type ECS struct {
	clusterName    string
	serviceName    string
	iamRoleArn     string
	taskDefinition *ecs.RegisterTaskDefinitionInput
	taskDefArn     *string
	targetGroupArn *string
}

// Task definition and Container definition
type TaskDefinition struct {
	Family               string                `json:"family"`
	Revision             int64                 `json:"revision"`
	ExecutionRoleArn     string                `json:"executionRole"`
	ContainerDefinitions []ContainerDefinition `json:"containerDefinitions"`
}
type ContainerDefinition struct {
	Name      string `json:"name"`
	Essential bool   `json:"essential"`
}

// containerInstance
type ContainerInstance struct {
	ContainerInstanceArn string
	Ec2InstanceId        string
	PendingTasksCount    int64
	RegisteredAt         time.Time
	RegisteredResources  []ContainerInstanceResource
	RemainingResources   []ContainerInstanceResource
	RunningTasksCount    int64
	Status               string
	Version              int64
}
type ContainerInstanceResource struct {
	DoubleValue    float64  `json:"doubleValue"`
	IntegerValue   int64    `json:"integerValue"`
	Name           string   `json:"name"`
	StringSetValue []string `json:"stringSetValue"`
	Type           string   `json:"type"`
}

// free instance resource
type FreeInstanceResource struct {
	InstanceId string
	Status     string
	FreeMemory int64
	FreeCpu    int64
}

// registered instance resource
type RegisteredInstanceResource struct {
	InstanceId       string
	RegisteredMemory int64
	RegisteredCpu    int64
}

// version info
type EcsVersionInfo struct {
	AgentHash     string `json:"agentHash"`
	AgentVersion  string `json:"agentVersion"`
	DockerVersion string `json:"dockerVersion"`
}

// create cluster
func (e *ECS) createCluster(clusterName string) (*string, error) {
	svc := ecs.New(session.New())
	createClusterInput := &ecs.CreateClusterInput{
		ClusterName: aws.String(clusterName),
	}

	result, err := svc.CreateCluster(createClusterInput)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			ecsLogger.Errorf("%v", aerr.Error())
		} else {
			ecsLogger.Errorf("%v", err.Error())
		}
		return nil, err
	}
	return result.Cluster.ClusterArn, nil
}
func (e *ECS) getECSAMI() (string, error) {
	var amiId string
	svc := ec2.New(session.New())
	input := &ec2.DescribeImagesInput{
		Owners: []*string{aws.String("591542846629")}, // AWS
		Filters: []*ec2.Filter{
			{Name: aws.String("name"), Values: []*string{aws.String("amzn-ami-*-amazon-ecs-optimized")}},
			{Name: aws.String("virtualization-type"), Values: []*string{aws.String("hvm")}},
		},
	}
	result, err := svc.DescribeImages(input)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			ecsLogger.Errorf("%v", aerr.Error())
		} else {
			ecsLogger.Errorf("%v", err.Error())
		}
		return amiId, err
	}
	if len(result.Images) == 0 {
		return amiId, errors.New("No ECS AMI found")
	}
	layout := "2006-01-02T15:04:05.000Z"
	var lastTime time.Time
	for _, v := range result.Images {
		t, err := time.Parse(layout, *v.CreationDate)
		if err != nil {
			return amiId, err
		}
		if t.After(lastTime) {
			lastTime = t
			amiId = *v.ImageId
		}
	}
	return amiId, nil
}
func (e *ECS) importKeyPair(keyName string, publicKey []byte) error {
	svc := ec2.New(session.New())
	input := &ec2.ImportKeyPairInput{
		KeyName:           aws.String(keyName),
		PublicKeyMaterial: publicKey,
	}
	_, err := svc.ImportKeyPair(input)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			ecsLogger.Errorf("%v", aerr.Error())
		} else {
			ecsLogger.Errorf("%v", err.Error())
		}
		return err
	}
	return nil
}
func (e *ECS) getPubKeyFromPrivateKey(privateKey string) ([]byte, error) {
	var pubASN1 []byte
	var key *rsa.PrivateKey
	block, _ := pem.Decode([]byte(privateKey))
	if block == nil {
		return pubASN1, errors.New("No private key found")
	}
	if block.Type != "RSA PRIVATE KEY" {
		return pubASN1, errors.New("Key not a RSA PRIVATE KEY")
	}
	key, err := x509.ParsePKCS1PrivateKey([]byte(block.Bytes))
	if err != nil {
		return pubASN1, err
	}
	pubASN1, err = x509.MarshalPKIXPublicKey(&key.PublicKey)
	if err != nil {
		return pubASN1, err
	}
	return []byte(base64.StdEncoding.EncodeToString(pubASN1)), nil
}
func (e *ECS) deleteKeyPair(keyName string) error {
	svc := ec2.New(session.New())
	input := &ec2.DeleteKeyPairInput{
		KeyName: aws.String(keyName),
	}
	_, err := svc.DeleteKeyPair(input)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			ecsLogger.Errorf("%v", aerr.Error())
		} else {
			ecsLogger.Errorf("%v", err.Error())
		}
		return err
	}
	return nil
}

// delete cluster
func (e *ECS) deleteCluster(clusterName string) error {
	svc := ecs.New(session.New())
	deleteClusterInput := &ecs.DeleteClusterInput{
		Cluster: aws.String(clusterName),
	}

	_, err := svc.DeleteCluster(deleteClusterInput)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			ecsLogger.Errorf("%v", aerr.Error())
		} else {
			ecsLogger.Errorf("%v", err.Error())
		}
		return err
	}
	return nil
}

// Creates ECS repository
func (e *ECS) createTaskDefinition(d Deploy) (*string, error) {
	svc := ecs.New(session.New())
	e.taskDefinition = &ecs.RegisterTaskDefinitionInput{
		Family:      aws.String(e.serviceName),
		TaskRoleArn: aws.String(e.iamRoleArn),
	}

	// set network mode if set
	if d.NetworkMode != "" {
		e.taskDefinition.SetNetworkMode(d.NetworkMode)
	}

	// placement constraints
	if len(d.PlacementConstraints) > 0 {
		var pcs []*ecs.TaskDefinitionPlacementConstraint
		for _, pc := range d.PlacementConstraints {
			tdpc := &ecs.TaskDefinitionPlacementConstraint{}
			if pc.Expression != "" {
				tdpc.SetExpression(pc.Expression)
			}
			if pc.Type != "" {
				tdpc.SetType(pc.Type)
			}
			pcs = append(pcs, tdpc)
		}
		e.taskDefinition.SetPlacementConstraints(pcs)
	}

	// loop over containers
	for _, container := range d.Containers {

		// get account id
		iam := IAM{}
		err := iam.getAccountId()
		if err != nil {
			return nil, errors.New("Could not get accountId during createTaskDefinition")
		}

		// prepare image Uri
		var imageUri string
		if container.ContainerURI == "" {
			if container.ContainerImage == "" {
				imageUri = iam.accountId + ".dkr.ecr." + util.GetEnv("AWS_REGION", "") + ".amazonaws.com" + "/" + container.ContainerName
			} else {
				imageUri = iam.accountId + ".dkr.ecr." + util.GetEnv("AWS_REGION", "") + ".amazonaws.com" + "/" + container.ContainerImage
			}
			if container.ContainerTag != "" {
				imageUri += ":" + container.ContainerTag
			}
		} else {
			imageUri = container.ContainerURI
		}

		// prepare container definition
		containerDefinition := &ecs.ContainerDefinition{
			Name:  aws.String(container.ContainerName),
			Image: aws.String(imageUri),
		}
		// set containerPort if not empty
		if container.ContainerPort > 0 {
			containerDefinition.SetPortMappings([]*ecs.PortMapping{
				{
					ContainerPort: aws.Int64(container.ContainerPort),
				},
			})
		}
		// set containerCommand if not empty
		if len(container.ContainerCommand) > 0 {
			containerDefinition.SetCommand(container.ContainerCommand)
		}
		// set cloudwacht logs if enabled
		if util.GetEnv("CLOUDWATCH_LOGS_ENABLED", "no") == "yes" {
			var logPrefix string
			if util.GetEnv("CLOUDWATCH_LOGS_PREFIX", "") != "" {
				logPrefix = util.GetEnv("CLOUDWATCH_LOGS_PREFIX", "") + "-" + util.GetEnv("AWS_ACCOUNT_ENV", "")
			}
			containerDefinition.SetLogConfiguration(&ecs.LogConfiguration{
				LogDriver: aws.String("awslogs"),
				Options: map[string]*string{
					"awslogs-group":         aws.String(logPrefix),
					"awslogs-region":        aws.String(util.GetEnv("AWS_REGION", "")),
					"awslogs-stream-prefix": aws.String(container.ContainerName),
				},
			})
		}
		if container.Memory > 0 {
			containerDefinition.Memory = aws.Int64(container.Memory)
		}
		if container.MemoryReservation > 0 {
			containerDefinition.MemoryReservation = aws.Int64(container.MemoryReservation)
		}
		if container.CPU > 0 {
			containerDefinition.Cpu = aws.Int64(container.CPU)
		} else {
			if container.CPU == 0 && util.GetEnv("DEFAULT_CONTAINER_CPU_LIMIT", "") != "" {
				defaultCpuLimit, err := strconv.ParseInt(util.GetEnv("DEFAULT_CONTAINER_CPU_LIMIT", ""), 10, 64)
				if err != nil {
					return nil, err
				}
				containerDefinition.Cpu = aws.Int64(defaultCpuLimit)
			}
		}

		if container.Essential {
			containerDefinition.Essential = aws.Bool(container.Essential)
		}

		if util.GetEnv("PARAMSTORE_ENABLED", "no") == "yes" {
			containerDefinition.SetEnvironment([]*ecs.KeyValuePair{
				{Name: aws.String("AWS_REGION"), Value: aws.String(util.GetEnv("AWS_REGION", ""))},
				{Name: aws.String("AWS_ENV_PATH"), Value: aws.String("/" + util.GetEnv("PARAMSTORE_PREFIX", "") + "-" + util.GetEnv("AWS_ACCOUNT_ENV", "") + "/" + e.serviceName + "/")},
			})
		}

		e.taskDefinition.ContainerDefinitions = append(e.taskDefinition.ContainerDefinitions, containerDefinition)
	}

	// going to register
	ecsLogger.Debugf("Going to register: %+v", e.taskDefinition)

	result, err := svc.RegisterTaskDefinition(e.taskDefinition)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			switch aerr.Code() {
			case ecs.ErrCodeServerException:
				ecsLogger.Errorf(ecs.ErrCodeServerException+": %v", aerr.Error())
			case ecs.ErrCodeClientException:
				ecsLogger.Errorf(ecs.ErrCodeClientException+": %v", aerr.Error())
			case ecs.ErrCodeInvalidParameterException:
				ecsLogger.Errorf(ecs.ErrCodeInvalidParameterException+": %v", aerr.Error())
			default:
				ecsLogger.Errorf(aerr.Error())
			}
		}
		// return error
		return nil, errors.New("Could not register task definition")
	} else {
		return result.TaskDefinition.TaskDefinitionArn, nil
	}
}

// check whether service exists
func (e *ECS) serviceExists(serviceName string) (bool, error) {
	svc := ecs.New(session.New())
	input := &ecs.DescribeServicesInput{
		Cluster: aws.String(e.clusterName),
		Services: []*string{
			aws.String(serviceName),
		},
	}

	result, err := svc.DescribeServices(input)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			switch aerr.Code() {
			case ecs.ErrCodeServerException:
				ecsLogger.Errorf(ecs.ErrCodeServerException, aerr.Error())
			case ecs.ErrCodeClientException:
				ecsLogger.Errorf(ecs.ErrCodeClientException, aerr.Error())
			case ecs.ErrCodeInvalidParameterException:
				ecsLogger.Errorf(ecs.ErrCodeInvalidParameterException, aerr.Error())
			case ecs.ErrCodeClusterNotFoundException:
				ecsLogger.Errorf(ecs.ErrCodeClusterNotFoundException, aerr.Error())
			default:
				ecsLogger.Errorf(aerr.Error())
			}
		} else {
			// Print the error, cast err to awserr.Error to get the Code and
			// Message from an error.
			ecsLogger.Errorf(err.Error())
		}
		return false, err
	}
	if len(result.Services) == 0 {
		return false, nil
	} else if len(result.Services) == 1 && *result.Services[0].Status == "INACTIVE" {
		return false, nil
	} else {
		return true, nil
	}
}

// Update ECS service
func (e *ECS) updateService(serviceName string, taskDefArn *string, d Deploy) (*string, error) {
	svc := ecs.New(session.New())
	input := &ecs.UpdateServiceInput{
		Cluster:        aws.String(e.clusterName),
		Service:        aws.String(serviceName),
		TaskDefinition: aws.String(*taskDefArn),
	}

	// set gracePeriodSeconds
	if d.HealthCheck.GracePeriodSeconds > 0 {
		input.SetHealthCheckGracePeriodSeconds(d.HealthCheck.GracePeriodSeconds)
	}

	ecsLogger.Debugf("Running UpdateService with input: %+v", input)

	result, err := svc.UpdateService(input)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			switch aerr.Code() {
			case ecs.ErrCodeServerException:
				ecsLogger.Errorf(ecs.ErrCodeServerException+": %v", aerr.Error())
			case ecs.ErrCodeClientException:
				ecsLogger.Errorf(ecs.ErrCodeClientException+": %v", aerr.Error())
			case ecs.ErrCodeInvalidParameterException:
				ecsLogger.Errorf(ecs.ErrCodeInvalidParameterException+": %v", aerr.Error())
			case ecs.ErrCodeClusterNotFoundException:
				ecsLogger.Errorf(ecs.ErrCodeClusterNotFoundException+": %v", aerr.Error())
			case ecs.ErrCodeServiceNotFoundException:
				ecsLogger.Infof(ecs.ErrCodeServiceNotFoundException+": %v", aerr.Error())
				// return error code to create new service
				return nil, errors.New("ServiceNotFoundException")
			case ecs.ErrCodeServiceNotActiveException:
				ecsLogger.Errorf(ecs.ErrCodeServiceNotActiveException+": %v", aerr.Error())
			default:
				ecsLogger.Errorf(aerr.Error())
			}
		} else {
			// Print the error, cast err to awserr.Error to get the Code and
			// Message from an error.
			ecsLogger.Errorf(err.Error())
		}
		return nil, errors.New("Could not update service: " + serviceName)
	}
	return result.Service.ServiceName, nil
}

// delete ECS service
func (e *ECS) deleteService(clusterName, serviceName string) error {
	// first set desiredCount to 0
	svc := ecs.New(session.New())
	input := &ecs.UpdateServiceInput{
		Cluster:      aws.String(clusterName),
		Service:      aws.String(serviceName),
		DesiredCount: aws.Int64(0),
	}

	_, err := svc.UpdateService(input)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			ecsLogger.Errorf("%v", aerr.Error())
		} else {
			ecsLogger.Errorf("%v", err.Error())
		}
		return err
	}
	// delete service
	input2 := &ecs.DeleteServiceInput{
		Cluster: aws.String(clusterName),
		Service: aws.String(serviceName),
	}

	_, err = svc.DeleteService(input2)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			ecsLogger.Errorf("%v", aerr.Error())
		} else {
			ecsLogger.Errorf("%v", err.Error())
		}
		return err
	}
	return nil
}

// create service
func (e *ECS) createService(d Deploy) error {
	svc := ecs.New(session.New())

	// sanity checks
	if len(d.Containers) == 0 {
		return errors.New("No containers defined")
	}

	input := &ecs.CreateServiceInput{
		Cluster:        aws.String(d.Cluster),
		DesiredCount:   aws.Int64(d.DesiredCount),
		ServiceName:    aws.String(e.serviceName),
		TaskDefinition: aws.String(*e.taskDefArn),
		PlacementStrategy: []*ecs.PlacementStrategy{
			{
				Field: aws.String("attribute:ecs.availability-zone"),
				Type:  aws.String("spread"),
			},
			{
				Field: aws.String("memory"),
				Type:  aws.String("binpack"),
			},
		},
	}

	if strings.ToLower(d.ServiceProtocol) != "none" {
		input.SetLoadBalancers([]*ecs.LoadBalancer{
			{
				ContainerName:  aws.String(e.serviceName),
				ContainerPort:  aws.Int64(d.ServicePort),
				TargetGroupArn: aws.String(*e.targetGroupArn),
			},
		})
	}

	// network configuration
	if d.NetworkMode == "awsvpc" && len(d.NetworkConfiguration.Subnets) > 0 {
		if strings.ToUpper(d.LaunchType) == "FARGATE" {
			input.SetLaunchType("FARGATE")
		}
		var sns []*string
		var sgs []*string
		var aIp string
		nc := &ecs.NetworkConfiguration{AwsvpcConfiguration: &ecs.AwsVpcConfiguration{}}
		for i, _ := range d.NetworkConfiguration.Subnets {
			sns = append(sns, &d.NetworkConfiguration.Subnets[i])
		}
		nc.AwsvpcConfiguration.SetSubnets(sns)
		for i, _ := range d.NetworkConfiguration.SecurityGroups {
			sgs = append(sgs, &d.NetworkConfiguration.SecurityGroups[i])
		}
		nc.AwsvpcConfiguration.SetSecurityGroups(sgs)
		if d.NetworkConfiguration.AssignPublicIp == "" {
			aIp = "DISABLED"
		} else {
			aIp = d.NetworkConfiguration.AssignPublicIp
		}
		nc.AwsvpcConfiguration.SetAssignPublicIp(aIp)
		input.SetNetworkConfiguration(nc)
	} else {
		// only set role if network mode is not awsvpc (it will be set automatically)
		if strings.ToLower(d.ServiceProtocol) != "none" { // only set the role if there's a loadbalancer necessary
			input.SetRole(util.GetEnv("AWS_ECS_SERVICE_ROLE", "ecs-service-role"))
		}
	}

	// check whether min/max is set
	dc := &ecs.DeploymentConfiguration{}
	if d.MinimumHealthyPercent > 0 {
		dc.SetMinimumHealthyPercent(d.MinimumHealthyPercent)
	}
	if d.MaximumPercent > 0 {
		dc.SetMaximumPercent(d.MaximumPercent)
	}
	if (ecs.DeploymentConfiguration{}) != *dc {
		input.SetDeploymentConfiguration(dc)
	}

	// set gracePeriodSeconds
	if d.HealthCheck.GracePeriodSeconds > 0 {
		input.SetHealthCheckGracePeriodSeconds(d.HealthCheck.GracePeriodSeconds)
	}

	// create service
	_, err := svc.CreateService(input)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			switch aerr.Code() {
			case ecs.ErrCodeServerException:
				ecsLogger.Errorf(ecs.ErrCodeServerException+": %v", aerr.Error())
			case ecs.ErrCodeClientException:
				ecsLogger.Errorf(ecs.ErrCodeClientException+": %v", aerr.Error())
			case ecs.ErrCodeInvalidParameterException:
				ecsLogger.Errorf(ecs.ErrCodeInvalidParameterException+": %v", aerr.Error())
			case ecs.ErrCodeClusterNotFoundException:
				ecsLogger.Errorf(ecs.ErrCodeClusterNotFoundException+": %v", aerr.Error())
			default:
				ecsLogger.Errorf(aerr.Error())
			}
		} else {
			ecsLogger.Errorf(err.Error())
		}
		return errors.New("Could not create service")
	}
	return nil
}

// wait until service is inactive
func (e *ECS) waitUntilServicesInactive(clusterName, serviceName string) error {
	svc := ecs.New(session.New())
	input := &ecs.DescribeServicesInput{
		Cluster:  aws.String(clusterName),
		Services: []*string{aws.String(serviceName)},
	}

	ecsLogger.Debugf("Waiting for service %v on %v to become inactive", serviceName, clusterName)

	err := svc.WaitUntilServicesInactive(input)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			ecsLogger.Errorf(aerr.Error())
		} else {
			ecsLogger.Errorf(err.Error())
		}
		return err
	}
	return nil
}

// wait until service is stable
func (e *ECS) waitUntilServicesStable(clusterName, serviceName string, maxWaitMinutes int) error {
	svc := ecs.New(session.New())
	maxAttempts := maxWaitMinutes * 4
	input := &ecs.DescribeServicesInput{
		Cluster:  aws.String(clusterName),
		Services: []*string{aws.String(serviceName)},
	}

	ecsLogger.Debugf("Waiting for service %v on %v to become stable", serviceName, clusterName)

	err := svc.WaitUntilServicesStableWithContext(context.Background(), input, request.WithWaiterMaxAttempts(maxAttempts))
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			ecsLogger.Errorf(aerr.Error())
		} else {
			ecsLogger.Errorf(err.Error())
		}
		return err
	}
	return nil
}
func (e *ECS) launchWaitUntilServicesStable(dd *DynamoDeployment) error {
	var failed bool
	var maxWaitMinutes int
	service := newService()
	// check whether service exists, otherwise wait might give error
	if dd.DeployData.HealthCheck.GracePeriodSeconds > 0 {
		maxWaitMinutes = (1 + int(math.Ceil(float64(dd.DeployData.HealthCheck.GracePeriodSeconds)/60/10))) * 10
	} else {
		maxWaitMinutes = 15
	}
	err := e.waitUntilServicesStable(dd.DeployData.Cluster, dd.ServiceName, maxWaitMinutes)
	if err != nil {
		ecsLogger.Debugf("waitUntilServiceStable didn't succeed: %v", err)
		failed = true
	}
	// check whether deployment has latest task definition
	runningService, err := e.describeService(dd.DeployData.Cluster, dd.ServiceName, false, true, true)
	if err != nil {
		return err
	}
	if len(runningService.Deployments) != 1 {
		reason := "Deployment failed: deployment was still running after 10 minutes"
		ecsLogger.Debugf(reason)
		err := service.setDeploymentStatusWithReason(dd, "failed", reason)
		if err != nil {
			return err
		}
		err = e.rollback(dd.DeployData.Cluster, dd.ServiceName)
		if err != nil {
			return err
		}
		return nil
	}
	if runningService.Deployments[0].TaskDefinition != *dd.TaskDefinitionArn {
		reason := "Deployment failed: Still running old task definition"
		ecsLogger.Debugf(reason)
		err := service.setDeploymentStatusWithReason(dd, "failed", reason)
		if err != nil {
			return err
		}
		err = e.rollback(dd.DeployData.Cluster, dd.ServiceName)
		if err != nil {
			return err
		}
		return nil
	}
	if len(runningService.Tasks) == 0 {
		reason := "Deployment failed: no tasks running"
		ecsLogger.Debugf(reason)
		err := service.setDeploymentStatusWithReason(dd, "failed", reason)
		if err != nil {
			return err
		}
		err = e.rollback(dd.DeployData.Cluster, dd.ServiceName)
		if err != nil {
			return err
		}
		return nil
	}
	for _, t := range runningService.Tasks {
		if t.TaskDefinitionArn == *dd.TaskDefinitionArn && t.LastStatus != "RUNNING" {
			reason := fmt.Sprintf("Deployment failed: found task with taskdefinition %v and status %v (expected RUNNING)", t.TaskDefinitionArn, t.LastStatus)
			ecsLogger.Debugf(reason)
			err := service.setDeploymentStatusWithReason(dd, "failed", reason)
			if err != nil {
				return err
			}
			err = e.rollback(dd.DeployData.Cluster, dd.ServiceName)
			if err != nil {
				return err
			}
			return nil
		}
		ecsLogger.Debugf("Found task with taskdefinition %v and status %v", t.TaskDefinitionArn, t.LastStatus)
	}
	if failed {
		service.setDeploymentStatusWithReason(dd, "failed", "Deployment timed out")
		return nil
	}
	// set success
	service.setDeploymentStatus(dd, "success")
	return nil
}
func (e *ECS) rollback(clusterName, serviceName string) error {
	ecsLogger.Debugf("Starting rollback")
	service := newService()
	service.serviceName = serviceName
	dd, err := service.getDeploys("secondToLast", 1)
	if err != nil {
		ecsLogger.Errorf("Error: %v", err.Error())
		return err
	}
	if len(dd) == 0 || dd[0].Status != "success" {
		ecsLogger.Debugf("Rollback: Previous deploy was not successful")
		dd, err := service.getDeploys("byMonth", 10)
		if err != nil {
			return err
		}
		ecsLogger.Debugf("Rollback: checking last %d deploys", len(dd))
	}
	for _, v := range dd {
		ecsLogger.Debugf("Looping previous deployments: %v with status %v", *v.TaskDefinitionArn, v.Status)
		if v.Status == "success" {
			ecsLogger.Debugf("Rollback: rolling back to %v", *v.TaskDefinitionArn)
			e.updateService(v.ServiceName, v.TaskDefinitionArn, *v.DeployData)
			return nil
		}
	}
	ecsLogger.Debugf("Could not rollback, no stable version found")
	return errors.New("Could not rollback, no stable version found")
}

// describe services
func (e *ECS) describeService(clusterName string, serviceName string, showEvents bool, showTasks bool, showStoppedTasks bool) (RunningService, error) {
	s, err := e.describeServices(clusterName, []*string{aws.String(serviceName)}, showEvents, showTasks, showStoppedTasks)
	if err == nil && len(s) == 1 {
		return s[0], nil
	} else {
		if err == nil {
			return RunningService{}, errors.New("describeService: No error, but array length != 1")
		} else {
			return RunningService{}, err
		}
	}
}
func (e *ECS) describeServices(clusterName string, serviceNames []*string, showEvents bool, showTasks bool, showStoppedTasks bool) ([]RunningService, error) {
	var rss []RunningService
	svc := ecs.New(session.New())

	// fetch per 10
	var y float64 = float64(len(serviceNames)) / 10
	for i := 0; i < int(math.Ceil(y)); i++ {

		f := i * 10
		t := int(math.Min(float64(10+10*i), float64(len(serviceNames))))

		input := &ecs.DescribeServicesInput{
			Cluster:  aws.String(clusterName),
			Services: serviceNames[f:t],
		}

		result, err := svc.DescribeServices(input)
		if err != nil {
			if aerr, ok := err.(awserr.Error); ok {
				ecsLogger.Errorf(aerr.Error())
			} else {
				ecsLogger.Errorf(err.Error())
			}
			return rss, err
		}
		for _, service := range result.Services {
			rs := RunningService{ServiceName: *service.ServiceName, ClusterName: clusterName}
			rs.RunningCount = *service.RunningCount
			rs.PendingCount = *service.PendingCount
			rs.DesiredCount = *service.DesiredCount
			rs.Status = *service.Status
			for _, deployment := range service.Deployments {
				var ds RunningServiceDeployment
				ds.Status = *deployment.Status
				ds.RunningCount = *deployment.RunningCount
				ds.PendingCount = *deployment.PendingCount
				ds.DesiredCount = *deployment.DesiredCount
				ds.CreatedAt = *deployment.CreatedAt
				ds.UpdatedAt = *deployment.UpdatedAt
				ds.TaskDefinition = *deployment.TaskDefinition
				rs.Deployments = append(rs.Deployments, ds)
			}
			if showEvents {
				for _, event := range service.Events {
					event := RunningServiceEvent{
						Id:        *event.Id,
						CreatedAt: *event.CreatedAt,
						Message:   *event.Message,
					}
					rs.Events = append(rs.Events, event)
				}
			}
			if showTasks {
				taskArns, err := e.listTasks(clusterName, *service.ServiceName, "RUNNING", "service")
				if err != nil {
					return rss, err
				}
				if showStoppedTasks {
					taskArnsStopped, err := e.listTasks(clusterName, *service.ServiceName, "STOPPED", "service")
					if err != nil {
						return rss, err
					}
					taskArns = append(taskArns, taskArnsStopped...)
				}
				runningTasks, err := e.describeTasks(clusterName, taskArns)
				if err != nil {
					return rss, err
				}
				rs.Tasks = runningTasks
			}
			rss = append(rss, rs)
		}
	}
	return rss, nil
}

// list tasks
func (e *ECS) listTasks(clusterName, name, desiredStatus, filterBy string) ([]*string, error) {
	svc := ecs.New(session.New())
	var tasks []*string

	input := &ecs.ListTasksInput{
		Cluster: aws.String(clusterName),
	}
	if filterBy == "service" {
		input.SetServiceName(name)
	} else if filterBy == "family" {
		input.SetFamily(name)
	} else {
		return tasks, errors.New("Invalid filterBy")
	}
	if desiredStatus == "STOPPED" {
		input.SetDesiredStatus(desiredStatus)
	}

	pageNum := 0
	err := svc.ListTasksPages(input,
		func(page *ecs.ListTasksOutput, lastPage bool) bool {
			pageNum++
			tasks = append(tasks, page.TaskArns...)
			return pageNum <= 100
		})

	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			ecsLogger.Errorf(aerr.Error())
		} else {
			ecsLogger.Errorf(err.Error())
		}
	}
	return tasks, err
}
func (e *ECS) describeTasks(clusterName string, tasks []*string) ([]RunningTask, error) {
	var rts []RunningTask
	svc := ecs.New(session.New())

	// fetch per 100
	var y float64 = float64(len(tasks)) / 100
	for i := 0; i < int(math.Ceil(y)); i++ {

		f := i * 100
		t := int(math.Min(float64(100+100*i), float64(len(tasks))))

		input := &ecs.DescribeTasksInput{
			Cluster: aws.String(clusterName),
			Tasks:   tasks[f:t],
		}

		result, err := svc.DescribeTasks(input)
		if err != nil {
			if aerr, ok := err.(awserr.Error); ok {
				ecsLogger.Errorf(aerr.Error())
			} else {
				ecsLogger.Errorf(err.Error())
			}
			return rts, err
		}
		for _, task := range result.Tasks {
			rs := RunningTask{}
			rs.ContainerInstanceArn = *task.ContainerInstanceArn
			rs.Cpu = *task.Cpu
			rs.CreatedAt = *task.CreatedAt
			rs.DesiredStatus = *task.DesiredStatus
			if task.ExecutionStoppedAt != nil {
				rs.ExecutionStoppedAt = *task.ExecutionStoppedAt
			}
			if task.Group != nil {
				rs.Group = *task.Group
			}
			rs.LastStatus = *task.LastStatus
			rs.LaunchType = *task.LaunchType
			rs.Memory = *task.Memory
			if task.PullStartedAt != nil {
				rs.PullStartedAt = *task.PullStartedAt
			}
			if task.PullStoppedAt != nil {
				rs.PullStoppedAt = *task.PullStoppedAt
			}
			if task.StartedAt != nil {
				rs.StartedAt = *task.StartedAt
			}
			if task.StartedBy != nil {
				rs.StartedBy = *task.StartedBy
			}
			if task.StoppedAt != nil {
				rs.StoppedAt = *task.StoppedAt
			}
			if task.StoppedReason != nil {
				rs.StoppedReason = *task.StoppedReason
			}
			if task.StoppingAt != nil {
				rs.StoppingAt = *task.StoppingAt
			}
			rs.TaskArn = *task.TaskArn
			rs.TaskDefinitionArn = *task.TaskDefinitionArn
			rs.Version = *task.Version
			for _, container := range task.Containers {
				var tc RunningTaskContainer
				tc.ContainerArn = *container.ContainerArn
				if container.ExitCode != nil {
					tc.ExitCode = *container.ExitCode
				}
				if container.LastStatus != nil {
					tc.LastStatus = *container.LastStatus
				}
				tc.Name = *container.Name
				if container.Reason != nil {
					tc.Reason = *container.Reason
				}
				rs.Containers = append(rs.Containers, tc)
			}
			rts = append(rts, rs)
		}
	}
	return rts, nil
}

func (e *ECS) listContainerInstances(clusterName string) ([]string, error) {
	svc := ecs.New(session.New())
	input := &ecs.ListContainerInstancesInput{
		Cluster: aws.String(clusterName),
	}
	var instanceArns []*string

	pageNum := 0
	err := svc.ListContainerInstancesPages(input,
		func(page *ecs.ListContainerInstancesOutput, lastPage bool) bool {
			pageNum++
			instanceArns = append(instanceArns, page.ContainerInstanceArns...)
			return pageNum <= 100
		})

	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			ecsLogger.Errorf("%v", aerr.Error())
		} else {
			ecsLogger.Errorf("%v", err.Error())
		}
		return aws.StringValueSlice(instanceArns), err
	}
	return aws.StringValueSlice(instanceArns), nil
}

// describe container instances
func (e *ECS) describeContainerInstances(clusterName string, containerInstances []string) ([]ContainerInstance, error) {
	var cis []ContainerInstance
	svc := ecs.New(session.New())
	input := &ecs.DescribeContainerInstancesInput{
		Cluster:            aws.String(clusterName),
		ContainerInstances: aws.StringSlice(containerInstances),
	}

	result, err := svc.DescribeContainerInstances(input)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			ecsLogger.Errorf(aerr.Error())
		} else {
			ecsLogger.Errorf(err.Error())
		}
		return cis, err
	}
	if len(result.ContainerInstances) == 0 {
		return cis, errors.New("No container instances returned")
	}
	for _, ci := range result.ContainerInstances {
		var c ContainerInstance
		c.ContainerInstanceArn = aws.StringValue(ci.ContainerInstanceArn)
		c.Ec2InstanceId = aws.StringValue(ci.Ec2InstanceId)
		c.PendingTasksCount = aws.Int64Value(ci.PendingTasksCount)
		c.RegisteredAt = aws.TimeValue(ci.RegisteredAt)
		c.RunningTasksCount = aws.Int64Value(ci.RunningTasksCount)
		c.Status = aws.StringValue(ci.Status)
		c.Version = aws.Int64Value(ci.Version)
		for _, v := range ci.RegisteredResources {
			var vv ContainerInstanceResource
			switch aws.StringValue(v.Type) {
			case "INTEGER":
				vv.IntegerValue = aws.Int64Value(v.IntegerValue)
			case "DOUBLE":
				vv.DoubleValue = aws.Float64Value(v.DoubleValue)
			case "LONG":
				vv.IntegerValue = aws.Int64Value(v.IntegerValue)
			case "STRINGSET":
				vv.StringSetValue = aws.StringValueSlice(v.StringSetValue)
			}
			vv.Name = aws.StringValue(v.Name)
			vv.Type = aws.StringValue(v.Type)
			c.RegisteredResources = append(c.RegisteredResources, vv)
		}
		for _, v := range ci.RemainingResources {
			var vv ContainerInstanceResource
			switch aws.StringValue(v.Type) {
			case "INTEGER":
				vv.IntegerValue = aws.Int64Value(v.IntegerValue)
			case "DOUBLE":
				vv.DoubleValue = aws.Float64Value(v.DoubleValue)
			case "LONG":
				vv.IntegerValue = aws.Int64Value(v.IntegerValue)
			case "STRINGSET":
				vv.StringSetValue = aws.StringValueSlice(v.StringSetValue)
			}
			vv.Name = aws.StringValue(v.Name)
			vv.Type = aws.StringValue(v.Type)
			c.RemainingResources = append(c.RemainingResources, vv)
		}
		cis = append(cis, c)
	}
	return cis, nil
}

// manual scale ECS service
func (e *ECS) manualScaleService(clusterName, serviceName string, desiredCount int64) error {
	svc := ecs.New(session.New())
	input := &ecs.UpdateServiceInput{
		Cluster:      aws.String(clusterName),
		Service:      aws.String(serviceName),
		DesiredCount: aws.Int64(desiredCount),
	}

	ecsLogger.Debugf("Manually scaling %v to a count of %d", serviceName, desiredCount)

	_, err := svc.UpdateService(input)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			ecsLogger.Errorf(aerr.Error())
		} else {
			ecsLogger.Errorf(err.Error())
		}
		return err
	}
	return nil
}

// run one-off task
func (e *ECS) runTask(clusterName, taskDefinition string, runTask RunTask) (string, error) {
	var taskArn string
	svc := ecs.New(session.New())
	input := &ecs.RunTaskInput{
		Cluster:        aws.String(clusterName),
		TaskDefinition: aws.String(taskDefinition),
		StartedBy:      aws.String(runTask.StartedBy),
	}

	taskOverride := &ecs.TaskOverride{}
	var containerOverrides []*ecs.ContainerOverride
	for _, co := range runTask.ContainerOverrides {
		containerOverrides = append(containerOverrides, &ecs.ContainerOverride{
			Command: aws.StringSlice(co.Command),
			Name:    aws.String(co.Name),
		})
	}
	taskOverride.SetContainerOverrides(containerOverrides)
	input.SetOverrides(taskOverride)

	ecsLogger.Debugf("Running ad-hoc task using taskdef %s and taskoverride: %+v", taskDefinition, taskOverride)

	result, err := svc.RunTask(input)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			ecsLogger.Errorf(aerr.Error())
		} else {
			ecsLogger.Errorf(err.Error())
		}
		return taskArn, err
	}
	if len(result.Tasks) == 0 {
		return taskArn, errors.New("No task arn returned")
	}
	return aws.StringValue(result.Tasks[0].TaskArn), nil
}

func (e *ECS) getTaskDefinition(clusterName, serviceName string) (string, error) {
	runningService, err := e.describeService(clusterName, serviceName, false, false, false)
	if err != nil {
		return "", nil
	}
	for _, d := range runningService.Deployments {
		if d.Status == "PRIMARY" {
			return d.TaskDefinition, nil
		}
	}
	return "", errors.New("No task definition found")
}
func (e *ECS) describeTaskDefinition(taskDefinitionNameOrArn string) (TaskDefinition, error) {
	var taskDefinition TaskDefinition
	svc := ecs.New(session.New())
	input := &ecs.DescribeTaskDefinitionInput{
		TaskDefinition: aws.String(taskDefinitionNameOrArn),
	}

	result, err := svc.DescribeTaskDefinition(input)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			ecsLogger.Errorf(aerr.Error())
		} else {
			ecsLogger.Errorf(err.Error())
		}
		return taskDefinition, err
	}

	taskDefinition.Family = aws.StringValue(result.TaskDefinition.Family)
	taskDefinition.Revision = aws.Int64Value(result.TaskDefinition.Revision)
	taskDefinition.ExecutionRoleArn = aws.StringValue(result.TaskDefinition.ExecutionRoleArn)
	var containerDefinitions []ContainerDefinition
	for _, cd := range result.TaskDefinition.ContainerDefinitions {
		var containerDefinition ContainerDefinition
		containerDefinition.Name = aws.StringValue(cd.Name)
		containerDefinition.Essential = aws.BoolValue(cd.Essential)
		containerDefinitions = append(containerDefinitions, containerDefinition)
	}
	taskDefinition.ContainerDefinitions = containerDefinitions

	return taskDefinition, nil
}

func (e *ECS) getContainerLimits(d Deploy) (int64, int64, int64, int64) {
	var cpuReservation, cpuLimit, memoryReservation, memoryLimit int64
	for _, c := range d.Containers {
		if c.MemoryReservation == 0 {
			memoryReservation += c.Memory
			memoryLimit += c.Memory
		} else {
			memoryReservation += c.MemoryReservation
			memoryLimit += c.Memory
		}
		if c.CPUReservation == 0 {
			cpuReservation += c.CPU
			cpuLimit += c.CPU
		} else {
			cpuReservation += c.CPUReservation
			cpuLimit += c.CPU
		}
	}
	return cpuReservation, cpuLimit, memoryReservation, memoryLimit
}
func (e *ECS) isEqualContainerLimits(d1 Deploy, d2 Deploy) bool {
	cpuReservation1, cpuLimit1, memoryReservation1, memoryLimit1 := e.getContainerLimits(d1)
	cpuReservation2, cpuLimit2, memoryReservation2, memoryLimit2 := e.getContainerLimits(d2)
	if cpuReservation1 == cpuReservation2 && cpuLimit1 == cpuLimit2 && memoryReservation1 == memoryReservation2 && memoryLimit1 == memoryLimit2 {
		return true
	} else {
		return false
	}
}

func (e *ECS) getFreeResources(clusterName string) ([]FreeInstanceResource, error) {
	var firs []FreeInstanceResource
	ciArns, err := e.listContainerInstances(clusterName)
	if err != nil {
		return firs, err
	}
	cis, err := e.describeContainerInstances(clusterName, ciArns)
	if err != nil {
		return firs, err
	}
	for _, ci := range cis {
		fir, err := e.convertResourceToFir(ci.RemainingResources)
		if err != nil {
			return firs, err
		}
		fir.InstanceId = ci.Ec2InstanceId
		fir.Status = ci.Status
		firs = append(firs, fir)
	}
	return firs, nil
}
func (e *ECS) convertResourceToFir(cir []ContainerInstanceResource) (FreeInstanceResource, error) {
	var fir FreeInstanceResource
	for _, v := range cir {
		if v.Name == "MEMORY" {
			if v.Type != "INTEGER" && v.Type != "LONG" {
				return fir, errors.New("Memory return wrong type (" + v.Type + ")")
			}
			fir.FreeMemory = v.IntegerValue
		}
		if v.Name == "CPU" {
			if v.Type != "INTEGER" && v.Type != "LONG" {
				return fir, errors.New("CPU return wrong type (" + v.Type + ")")
			}
			fir.FreeCpu = v.IntegerValue
		}
	}
	return fir, nil
}
func (e *ECS) convertResourceToRir(cir []ContainerInstanceResource) (RegisteredInstanceResource, error) {
	var rir RegisteredInstanceResource
	for _, v := range cir {
		if v.Name == "MEMORY" {
			if v.Type != "INTEGER" && v.Type != "LONG" {
				return rir, errors.New("Memory return wrong type (" + v.Type + ")")
			}
			rir.RegisteredMemory = v.IntegerValue
		}
		if v.Name == "CPU" {
			if v.Type != "INTEGER" && v.Type != "LONG" {
				return rir, errors.New("CPU return wrong type (" + v.Type + ")")
			}
			rir.RegisteredCpu = v.IntegerValue
		}
	}
	return rir, nil
}

func (e *ECS) drainNode(clusterName, instance string) error {
	svc := ecs.New(session.New())
	input := &ecs.UpdateContainerInstancesStateInput{
		Cluster:            aws.String(clusterName),
		ContainerInstances: aws.StringSlice([]string{instance}),
		Status:             aws.String("DRAINING"),
	}
	_, err := svc.UpdateContainerInstancesState(input)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			ecsLogger.Errorf("%v", aerr.Error())
		} else {
			ecsLogger.Errorf("%v", err.Error())
		}
		return err
	}
	return nil
}
func (e *ECS) getClusterNameByInstanceId(instance string) (string, error) {
	var clusterName string
	svc := ec2.New(session.New())
	input := &ec2.DescribeTagsInput{
		Filters: []*ec2.Filter{
			{
				Name: aws.String("resource-id"),
				Values: []*string{
					aws.String(instance),
				},
			},
		},
	}

	result, err := svc.DescribeTags(input)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			ecsLogger.Errorf("%v", aerr.Error())
		} else {
			ecsLogger.Errorf("%v", err.Error())
		}
		return clusterName, err
	}
	for _, v := range result.Tags {
		if aws.StringValue(v.Key) == "Cluster" {
			return aws.StringValue(v.Value), nil
		}
	}
	return clusterName, errors.New("Could not determine clusterName. Is the EC2 instance tagged?")
}
func (e *ECS) getContainerInstanceArnByInstanceId(clusterName, instanceId string) (string, error) {
	ciArns, err := e.listContainerInstances(clusterName)
	if err != nil {
		return "", err
	}
	cis, err := e.describeContainerInstances(clusterName, ciArns)
	if err != nil {
		return "", err
	}
	for _, ci := range cis {
		if ci.Ec2InstanceId == instanceId {
			return ci.ContainerInstanceArn, nil
		}
	}
	return "", errors.New("Couldn't find container instance Arn (instanceId=" + instanceId + ")")
}
func (e *ECS) launchWaitForDrainedNode(clusterName, containerInstanceArn, instanceId, autoScalingGroupName, lifecycleHookName, lifecycleHookToken string) error {
	var tasksDrained bool
	var err error
	for i := 0; i < 80 && !tasksDrained; i++ {
		cis, err := e.describeContainerInstances(clusterName, []string{containerInstanceArn})
		if err != nil || len(cis) == 0 {
			ecsLogger.Errorf("launchWaitForDrainedNode: %v", err.Error())
			return err
		}
		ci := cis[0]
		if ci.RunningTasksCount == 0 {
			tasksDrained = true
		} else {
			ecsLogger.Infof("launchWaitForDrainedNode: still %d tasks running", ci.RunningTasksCount)
		}
		time.Sleep(15 * time.Second)
	}
	if !tasksDrained {
		ecsLogger.Errorf("launchWaitForDrainedNode: Not able to drain tasks: timeout of 20m reached")
	}
	// CompleteLifeCycleAction
	autoscaling := AutoScaling{}
	if lifecycleHookToken == "" {
		ecsLogger.Debugf("Running completePendingLifecycleAction")
		err = autoscaling.completePendingLifecycleAction(autoScalingGroupName, instanceId, "CONTINUE", lifecycleHookName)
	} else {
		ecsLogger.Debugf("Running completeLifecycleAction")
		err = autoscaling.completeLifecycleAction(autoScalingGroupName, instanceId, "CONTINUE", lifecycleHookName, lifecycleHookToken)
	}
	if err != nil {
		ecsLogger.Errorf("launchWaitForDrainedNode: Could not complete life cycle action: %v", err.Error())
		return err
	}
	ecsLogger.Infof("launchWaitForDrainedNode: Node drained, completed lifecycle action")
	return nil
}

// list services
func (e *ECS) listServices(clusterName string) ([]*string, error) {
	svc := ecs.New(session.New())
	var services []*string

	input := &ecs.ListServicesInput{
		Cluster: aws.String(clusterName),
	}

	pageNum := 0
	err := svc.ListServicesPages(input,
		func(page *ecs.ListServicesOutput, lastPage bool) bool {
			pageNum++
			services = append(services, page.ServiceArns...)
			return pageNum <= 100
		})

	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			ecsLogger.Errorf(aerr.Error())
		} else {
			ecsLogger.Errorf(err.Error())
		}
	}
	return services, err
}
