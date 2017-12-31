package main

import (
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/credentials/stscreds"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/iam"
	"github.com/aws/aws-sdk-go/service/sts"
	"github.com/juju/loggo"

	"encoding/json"
	"errors"
	"fmt"
)

// logging
var iamLogger = loggo.GetLogger("iam")

// IAM struct
type IAM struct {
	stsAssumingRole *sts.STS
	accountId       string
}

// default IAM trust

func (e *IAM) getEcsTaskIAMTrust() string {
	return `{ "Version": "2012-10-17", "Statement": [ { "Action": "sts:AssumeRole", "Principal": { "Service": "ecs-tasks.amazonaws.com" }, "Effect": "Allow" } ] }`
}
func (e *IAM) getEcsServiceIAMTrust() string {
	return `{ "Version": "2012-10-17", "Statement": [ { "Action": "sts:AssumeRole", "Principal": { "Service": "ecs.amazonaws.com" }, "Effect": "Allow" } ] }`
}
func (e *IAM) getEcsServicePolicy() string {
	return `arn:aws:iam::aws:policy/service-role/AmazonEC2ContainerServiceRole`
}

func (e *IAM) getAccountId() error {
	var svc *sts.STS
	if e.stsAssumingRole == nil {
		svc = sts.New(session.New())
	} else {
		svc = e.stsAssumingRole
	}
	input := &sts.GetCallerIdentityInput{}

	result, err := svc.GetCallerIdentity(input)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			switch aerr.Code() {
			default:
				iamLogger.Errorf(aerr.Error())
			}
		} else {
			iamLogger.Errorf(err.Error())
		}
		return errors.New("Couldn't get caller identity")
	}
	e.accountId = *result.Account
	return nil
}

func (e *IAM) roleExists(roleName string) (*string, error) {
	svc := iam.New(session.New())
	input := &iam.GetRoleInput{
		RoleName: aws.String(roleName),
	}

	result, err := svc.GetRole(input)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			switch aerr.Code() {
			case iam.ErrCodeNoSuchEntityException:
				return nil, nil
			case iam.ErrCodeServiceFailureException:
				iamLogger.Errorf(iam.ErrCodeServiceFailureException+": %v", aerr.Error())
			default:
				iamLogger.Errorf(aerr.Error())
			}
		} else {
			iamLogger.Errorf(err.Error())
		}
		return nil, errors.New(fmt.Sprintf("Could not retrieve role: %v (check AWS credentials)", roleName))
	}
	return result.Role.Arn, nil
}

func (e *IAM) createRole(roleName, assumePolicyDocument string) (*string, error) {
	svc := iam.New(session.New())
	input := &iam.CreateRoleInput{
		AssumeRolePolicyDocument: aws.String(assumePolicyDocument),
		Path:     aws.String("/"),
		RoleName: aws.String(roleName),
	}

	result, err := svc.CreateRole(input)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			switch aerr.Code() {
			case iam.ErrCodeLimitExceededException:
				iamLogger.Errorf(iam.ErrCodeLimitExceededException+": %v", aerr.Error())
			case iam.ErrCodeInvalidInputException:
				iamLogger.Errorf(iam.ErrCodeInvalidInputException+": %v", aerr.Error())
			case iam.ErrCodeEntityAlreadyExistsException:
				iamLogger.Errorf(iam.ErrCodeEntityAlreadyExistsException+": %v", aerr.Error())
			case iam.ErrCodeMalformedPolicyDocumentException:
				iamLogger.Errorf(iam.ErrCodeMalformedPolicyDocumentException+": %v", aerr.Error())
			case iam.ErrCodeServiceFailureException:
				iamLogger.Errorf(iam.ErrCodeServiceFailureException+": %v", aerr.Error())
			default:
				iamLogger.Errorf(aerr.Error())
			}
		} else {
			// Print the error, cast err to awserr.Error to get the Code and
			// Message from an error.
			iamLogger.Errorf(err.Error())
		}
		// return error
		return nil, errors.New(fmt.Sprintf("Could not create role: %v", roleName))
	} else {
		return result.Role.Arn, nil
	}
}

func (e *IAM) putRolePolicy(roleName, policyName, policy string) error {
	svc := iam.New(session.New())

	input := &iam.PutRolePolicyInput{
		PolicyDocument: aws.String(policy),
		PolicyName:     aws.String(policyName),
		RoleName:       aws.String(roleName),
	}

	_, err := svc.PutRolePolicy(input)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			switch aerr.Code() {
			case iam.ErrCodeLimitExceededException:
				iamLogger.Errorf(iam.ErrCodeLimitExceededException+": %v", aerr.Error())
			case iam.ErrCodeMalformedPolicyDocumentException:
				iamLogger.Errorf(iam.ErrCodeMalformedPolicyDocumentException+": %v", aerr.Error())
			case iam.ErrCodeNoSuchEntityException:
				iamLogger.Errorf(iam.ErrCodeNoSuchEntityException+": %v", aerr.Error())
			case iam.ErrCodeUnmodifiableEntityException:
				iamLogger.Errorf(iam.ErrCodeUnmodifiableEntityException+": %v", aerr.Error())
			case iam.ErrCodeServiceFailureException:
				iamLogger.Errorf(iam.ErrCodeServiceFailureException+": %v", aerr.Error())
			default:
				iamLogger.Errorf(aerr.Error())
			}
		} else {
			iamLogger.Errorf(err.Error())
		}
		return errors.New(fmt.Sprintf("Could not put role policy for: %v", roleName))
	}
	return nil
}
func (e *IAM) attachRolePolicy(roleName, policyArn string) error {
	svc := iam.New(session.New())
	input := &iam.AttachRolePolicyInput{
		PolicyArn: aws.String(policyArn),
		RoleName:  aws.String(roleName),
	}

	_, err := svc.AttachRolePolicy(input)
	if err != nil {
		if aerr, ok := err.(awserr.Error); ok {
			switch aerr.Code() {
			case iam.ErrCodeNoSuchEntityException:
				iamLogger.Errorf(iam.ErrCodeNoSuchEntityException, aerr.Error())
			case iam.ErrCodeLimitExceededException:
				iamLogger.Errorf(iam.ErrCodeLimitExceededException, aerr.Error())
			case iam.ErrCodeInvalidInputException:
				iamLogger.Errorf(iam.ErrCodeInvalidInputException, aerr.Error())
			case iam.ErrCodeUnmodifiableEntityException:
				iamLogger.Errorf(iam.ErrCodeUnmodifiableEntityException, aerr.Error())
			case iam.ErrCodePolicyNotAttachableException:
				iamLogger.Errorf(iam.ErrCodePolicyNotAttachableException, aerr.Error())
			case iam.ErrCodeServiceFailureException:
				iamLogger.Errorf(iam.ErrCodeServiceFailureException, aerr.Error())
			default:
				iamLogger.Errorf(aerr.Error())
			}
		} else {
			// Print the error, cast err to awserr.Error to get the Code and
			// Message from an error.
			iamLogger.Errorf(err.Error())
		}
		return errors.New("Could not attach role policy to role")
	}
	return nil
}

func (e *IAM) assumeRole(roleArn, roleSessionName, prevCreds string) (*credentials.Credentials, string, error) {
	sess := session.Must(session.NewSession())
	// check previous credentials
	var value credentials.Value
	var creds *credentials.Credentials
	if prevCreds != "" {
		iamLogger.Debugf("Found previous credentials")
		err := json.Unmarshal([]byte(prevCreds), &value)
		if err == nil {
			iamLogger.Debugf("Unmarshalled previous credentials")
			creds = credentials.NewStaticCredentialsFromCreds(value)
			// test old credentials
			e.stsAssumingRole = sts.New(sess, &aws.Config{Credentials: creds})
			if e.stsAssumingRole == nil {
				return creds, "", errors.New("Could not assume role")
			}
			err = e.getAccountId()
			if err != nil {
				iamLogger.Debugf("Credentials are expired")
				creds = nil
			}
		}
	}
	// retrieve new credentials for roleArn
	if creds == nil {
		iamLogger.Debugf("Using new credentials")
		creds = stscreds.NewCredentials(sess, roleArn, func(a *stscreds.AssumeRoleProvider) {
			a.RoleSessionName = roleSessionName
		})
	}
	// convert credentials to json
	valCreds, err := creds.Get()
	if err != nil {
		return creds, "", err
	}
	jsonCreds, err := json.Marshal(valCreds)
	if err != nil {
		return creds, "", err
	}
	return creds, string(jsonCreds), nil
}
