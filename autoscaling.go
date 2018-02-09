package ecsdeploy

import (
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/autoscaling"
	"github.com/juju/loggo"

	"encoding/base64"
	"errors"
	"strings"
)

// logging
var autoscalingLogger = loggo.GetLogger("autoscaling")

// ECR struct
type AutoScaling struct {
}

func (a *AutoScaling) completeLifecycleAction(autoScalingGroupName, instanceId, action, lifecycleHookName, lifecycleToken string) error {
	svc := autoscaling.New(session.New())
	input := &autoscaling.CompleteLifecycleActionInput{
		AutoScalingGroupName:  aws.String(autoScalingGroupName),
		InstanceId:            aws.String(instanceId),
		LifecycleActionResult: aws.String(action),
		LifecycleActionToken:  aws.String(lifecycleToken),
		LifecycleHookName:     aws.String(lifecycleHookName),
	}

	_, err := svc.CompleteLifecycleAction(input)
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
func (a *AutoScaling) completePendingLifecycleAction(autoScalingGroupName, instanceId, action, lifecycleHookName string) error {
	svc := autoscaling.New(session.New())
	input := &autoscaling.CompleteLifecycleActionInput{
		AutoScalingGroupName:  aws.String(autoScalingGroupName),
		InstanceId:            aws.String(instanceId),
		LifecycleActionResult: aws.String(action),
		LifecycleHookName:     aws.String(lifecycleHookName),
	}

	autoscalingLogger.Debugf("Running CompleteLifecycleAction with input: %+v", input)

	_, err := svc.CompleteLifecycleAction(input)
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
func (a *AutoScaling) getLifecycleHookNames(autoScalingGroupName, lifecycleHookType string) ([]string, error) {
	var lifecycleHookNames []string
	svc := autoscaling.New(session.New())
	input := &autoscaling.DescribeLifecycleHooksInput{
		AutoScalingGroupName: aws.String(autoScalingGroupName),
	}

	result, err := svc.DescribeLifecycleHooks(input)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			ecsLogger.Errorf("%v", aerr.Error())
		} else {
			ecsLogger.Errorf("%v", err.Error())
		}
		return lifecycleHookNames, err
	}
	if len(result.LifecycleHooks) == 0 {
		return lifecycleHookNames, errors.New("No life cycle hooks returned")
	}
	for _, v := range result.LifecycleHooks {
		if aws.StringValue(v.LifecycleTransition) == lifecycleHookType {
			lifecycleHookNames = append(lifecycleHookNames, aws.StringValue(v.LifecycleHookName))
		}
	}
	return lifecycleHookNames, nil
}

func (a *AutoScaling) createLaunchConfiguration(clusterName string, keyName string, instanceType string, instanceProfile string, securitygroups []string) error {
	ecs := ECS{}
	svc := autoscaling.New(session.New())
	amiId, err := ecs.getECSAMI()
	if err != nil {
		return err
	}
	input := &autoscaling.CreateLaunchConfigurationInput{
		IamInstanceProfile:      aws.String(instanceProfile),
		ImageId:                 aws.String(amiId),
		InstanceType:            aws.String(instanceType),
		KeyName:                 aws.String(keyName),
		LaunchConfigurationName: aws.String(clusterName),
		SecurityGroups:          aws.StringSlice(securitygroups),
		UserData:                aws.String(base64.StdEncoding.EncodeToString([]byte("#!/bin/bash\necho 'ECS_CLUSTER=" + clusterName + "'  > /etc/ecs/ecs.config\nstart ecs\n"))),
	}
	ecsLogger.Debugf("createLaunchConfiguration with: %+v", input)
	_, err = svc.CreateLaunchConfiguration(input)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			if strings.Contains(aerr.Message(), "Invalid IamInstanceProfile") {
				ecsLogger.Debugf("Caught RetryableError: %v", aerr.Message())
				return errors.New("RetryableError: Invalid IamInstanceProfile")
			} else {
				ecsLogger.Errorf("%v", aerr.Error())
			}
		} else {
			ecsLogger.Errorf("%v", err.Error())
		}
		return err
	}
	return nil
}
func (a *AutoScaling) deleteLaunchConfiguration(clusterName string) error {
	svc := autoscaling.New(session.New())
	input := &autoscaling.DeleteLaunchConfigurationInput{
		LaunchConfigurationName: aws.String(clusterName),
	}
	_, err := svc.DeleteLaunchConfiguration(input)
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
func (a *AutoScaling) createAutoScalingGroup(clusterName string, desiredCapacity int64, maxSize int64, minSize int64, subnets []string) error {
	svc := autoscaling.New(session.New())
	input := &autoscaling.CreateAutoScalingGroupInput{
		AutoScalingGroupName:    aws.String(clusterName),
		DesiredCapacity:         aws.Int64(desiredCapacity),
		HealthCheckType:         aws.String("EC2"),
		LaunchConfigurationName: aws.String(clusterName),
		MaxSize:                 aws.Int64(maxSize),
		MinSize:                 aws.Int64(minSize),
		Tags: []*autoscaling.Tag{
			{Key: aws.String("Name"), Value: aws.String("ecs-" + clusterName), PropagateAtLaunch: aws.Bool(true)},
			{Key: aws.String("Cluster"), Value: aws.String(clusterName), PropagateAtLaunch: aws.Bool(true)},
		},
		TerminationPolicies: []*string{aws.String("OldestLaunchConfiguration"), aws.String("Default")},
		VPCZoneIdentifier:   aws.String(strings.Join(subnets, ",")),
	}
	_, err := svc.CreateAutoScalingGroup(input)
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
func (a *AutoScaling) waitForAutoScalingGroupInService(clusterName string) error {
	svc := autoscaling.New(session.New())
	input := &autoscaling.DescribeAutoScalingGroupsInput{
		AutoScalingGroupNames: []*string{aws.String(clusterName)},
	}
	err := svc.WaitUntilGroupInService(input)
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
func (a *AutoScaling) waitForAutoScalingGroupNotExists(clusterName string) error {
	svc := autoscaling.New(session.New())
	input := &autoscaling.DescribeAutoScalingGroupsInput{
		AutoScalingGroupNames: []*string{aws.String(clusterName)},
	}
	err := svc.WaitUntilGroupNotExists(input)
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
func (a *AutoScaling) deleteAutoScalingGroup(clusterName string, forceDelete bool) error {
	svc := autoscaling.New(session.New())
	input := &autoscaling.DeleteAutoScalingGroupInput{
		AutoScalingGroupName: aws.String(clusterName),
		ForceDelete:          aws.Bool(forceDelete),
	}
	_, err := svc.DeleteAutoScalingGroup(input)
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
func (a *AutoScaling) scaleClusterNodes(autoScalingGroupName string, change int64) error {
	minSize, desiredCapacity, maxSize, err := a.getClusterNodeDesiredCount(autoScalingGroupName)
	if err != nil {
		return err
	}
	if change > 0 && desiredCapacity == maxSize {
		return errors.New("Cluster is at maximum capacity")
	}
	if change < 0 && desiredCapacity == minSize {
		return errors.New("Cluster is at minimum capacity")
	}

	svc := autoscaling.New(session.New())
	input := &autoscaling.UpdateAutoScalingGroupInput{
		AutoScalingGroupName: aws.String(autoScalingGroupName),
		DesiredCapacity:      aws.Int64(desiredCapacity + change),
	}
	_, err = svc.UpdateAutoScalingGroup(input)
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
func (a *AutoScaling) getClusterNodeDesiredCount(autoScalingGroupName string) (int64, int64, int64, error) {
	svc := autoscaling.New(session.New())
	input := &autoscaling.DescribeAutoScalingGroupsInput{
		AutoScalingGroupNames: []*string{aws.String(autoScalingGroupName)},
	}
	result, err := svc.DescribeAutoScalingGroups(input)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			ecsLogger.Errorf("%v", aerr.Error())
		} else {
			ecsLogger.Errorf("%v", err.Error())
		}
		return 0, 0, 0, err
	}
	if len(result.AutoScalingGroups) == 0 {
		return 0, 0, 0, errors.New("No autoscaling groups returned")
	}

	return aws.Int64Value(result.AutoScalingGroups[0].MinSize),
		aws.Int64Value(result.AutoScalingGroups[0].DesiredCapacity),
		aws.Int64Value(result.AutoScalingGroups[0].MaxSize),
		nil
}
func (a *AutoScaling) getAutoScalingGroupByTag(clusterName string) (string, error) {
	var result string
	svc := autoscaling.New(session.New())
	input := &autoscaling.DescribeAutoScalingGroupsInput{}
	pageNum := 0
	err := svc.DescribeAutoScalingGroupsPages(input,
		func(page *autoscaling.DescribeAutoScalingGroupsOutput, lastPage bool) bool {
			pageNum++
			for _, v := range page.AutoScalingGroups {
				for _, tag := range v.Tags {
					if aws.StringValue(tag.Key) == "Cluster" && aws.StringValue(tag.Value) == clusterName {
						result = aws.StringValue(v.AutoScalingGroupName)
					}
				}
			}
			return pageNum <= 100
		})

	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			ecsLogger.Errorf(aerr.Error())
		} else {
			ecsLogger.Errorf(err.Error())
		}
	}
	if result == "" {
		return result, errors.New("ClusterNotFound: Could not find cluster by tag key=Cluster,Value=" + clusterName)
	}
	return result, nil
}
