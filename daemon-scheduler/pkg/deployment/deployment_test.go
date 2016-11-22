// Copyright 2016 Amazon.com, Inc. or its affiliates. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License"). You may
// not use this file except in compliance with the License. A copy of the
// License is located at
//
//     http://aws.amazon.com/apache2.0/
//
// or in the "license" file accompanying this file. This file is distributed
// on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either
// express or implied. See the License for the specific language governing
// permissions and limitations under the License.

package deployment

import (
	"context"
	"reflect"
	"testing"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/ecs"
	"github.com/blox/blox/daemon-scheduler/pkg/clients/css/models"
	"github.com/blox/blox/daemon-scheduler/pkg/mocks"
	"github.com/blox/blox/daemon-scheduler/pkg/types"
	"github.com/golang/mock/gomock"
	"github.com/pborman/uuid"
	"github.com/pkg/errors"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
)

const (
	deploymentID = "deploymentID"
	instanceARN1 = "arn:aws:us-east-1:123456789123:container-instance/4b6d45ea-a4b4-4269-9d04-3af6ddfdc597"
	instanceARN2 = "arn:aws:us-east-1:123456789123:container-instance/15a1c3d8-e449-4377-9aed-affafb3da5eb"
)

var (
	environmentVersion = uuid.NewRandom().String()
)

type DeploymentTestSuite struct {
	suite.Suite
	environment           *mocks.MockEnvironment
	clusterState          *mocks.MockClusterState
	ecs                   *mocks.MockECS
	deployment            Deployment
	ctx                   context.Context
	deploymentEnvironment *types.Environment
	instanceARNs          []*string
	startTaskOutput       *ecs.StartTaskOutput
}

func (suite *DeploymentTestSuite) SetupTest() {
	mockCtrl := gomock.NewController(suite.T())

	suite.environment = mocks.NewMockEnvironment(mockCtrl)
	suite.clusterState = mocks.NewMockClusterState(mockCtrl)
	suite.ecs = mocks.NewMockECS(mockCtrl)
	suite.deployment = NewDeployment(suite.environment, suite.clusterState, suite.ecs)

	var err error
	suite.deploymentEnvironment, err = types.NewEnvironment(environmentName, taskDefinition, cluster)
	assert.Nil(suite.T(), err, "Cannot initialize DeploymentTestSuite")

	suite.instanceARNs = []*string{aws.String(instanceARN1), aws.String(instanceARN2)}

	task := ecs.Task{
		TaskArn:           aws.String(taskARN1),
		TaskDefinitionArn: aws.String(taskDefinition),
	}

	failure := ecs.Failure{
		Arn: aws.String(instanceARN1),
	}

	suite.startTaskOutput = &ecs.StartTaskOutput{
		Tasks:    []*ecs.Task{&task},
		Failures: []*ecs.Failure{&failure},
	}

	suite.ctx = context.TODO()
}

func TestDeploymentTestSuite(t *testing.T) {
	suite.Run(t, new(DeploymentTestSuite))
}

func (suite *DeploymentTestSuite) TestNewDeployment() {
	d := NewDeployment(suite.environment, suite.clusterState, suite.ecs)
	assert.NotNil(suite.T(), d, "Expected an error when store is nil")
}

func (suite *DeploymentTestSuite) TestCreateDeploymentEmptyEnvironmentName() {
	_, err := suite.deployment.CreateDeployment(suite.ctx, "", environmentVersion)
	assert.Error(suite.T(), err, "Expected an error when environment name is empty")
}

func (suite *DeploymentTestSuite) TestCreateDeploymentGetEnvironmentFails() {
	suite.environment.EXPECT().GetEnvironment(suite.ctx, environmentName).
		Return(nil, errors.New("Get environment failed"))

	_, err := suite.deployment.CreateDeployment(suite.ctx, environmentName, environmentVersion)
	assert.Error(suite.T(), err, "Expected an error when get environment fails")
}

func (suite *DeploymentTestSuite) TestCreateDeploymentGetEnvironmentIsNil() {
	suite.environment.EXPECT().GetEnvironment(suite.ctx, environmentName).
		Return(nil, nil)

	_, err := suite.deployment.CreateDeployment(suite.ctx, environmentName, environmentVersion)
	assert.Error(suite.T(), err, "Expected an error when get environment is nil")
}

func (suite *DeploymentTestSuite) TestCreateDeploymentOutdatedToken() {
	suite.environment.EXPECT().GetEnvironment(suite.ctx, environmentName).
		Return(suite.deploymentEnvironment, nil)

	_, err := suite.deployment.CreateDeployment(suite.ctx, environmentName, "invalid")
	assert.Error(suite.T(), err, "Expected an error when token is outdated")
}

func (suite *DeploymentTestSuite) TestCreateDeploymentAddDeploymentFails() {
	suite.environment.EXPECT().GetEnvironment(suite.ctx, environmentName).
		Return(suite.deploymentEnvironment, nil)

	deployment, err := types.NewDeployment(suite.deploymentEnvironment.DesiredTaskDefinition)
	assert.Nil(suite.T(), err, "Deployment creation failed")

	suite.environment.EXPECT().AddDeployment(suite.ctx, *suite.deploymentEnvironment, gomock.Any()).Do(
		func(_ interface{}, _ interface{}, d types.Deployment) {
			verifyDeployment(suite.T(), deployment, &d)
		}).Return(nil, errors.New("Add deployment failed"))

	_, err = suite.deployment.CreateDeployment(suite.ctx, environmentName, suite.deploymentEnvironment.Token)
	assert.Error(suite.T(), err, "Expected an error when add deployment fails")
}

func (suite *DeploymentTestSuite) TestCreateDeploymentThereIsAnInProgressDeployment() {
	suite.environment.EXPECT().GetEnvironment(suite.ctx, environmentName).Return(suite.deploymentEnvironment, nil)

	deployment, err := types.NewDeployment(suite.deploymentEnvironment.DesiredTaskDefinition)
	assert.Nil(suite.T(), err, "Deployment creation failed")

	inprogressDeployment := &types.Deployment{
		ID:     uuid.NewRandom().String(),
		Status: types.DeploymentInProgress,
	}

	suite.deploymentEnvironment.Deployments[deployment.ID] = *deployment
	suite.deploymentEnvironment.Deployments[inprogressDeployment.ID] = *inprogressDeployment

	suite.environment.EXPECT().AddDeployment(suite.ctx, *suite.deploymentEnvironment, gomock.Any()).Do(
		func(_ interface{}, _ interface{}, d types.Deployment) {
			verifyDeployment(suite.T(), deployment, &d)
		}).Return(suite.deploymentEnvironment, nil)

	suite.clusterState.EXPECT().ListInstances(gomock.Any()).Times(0)
	suite.ecs.EXPECT().StartTask(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
	suite.environment.EXPECT().UpdateDeployment(suite.ctx, gomock.Any(), gomock.Any()).Times(0)

	d, err := suite.deployment.CreateDeployment(suite.ctx, environmentName, suite.deploymentEnvironment.Token)
	assert.Nil(suite.T(), err, "Unexpected error when there is an in-progress deployment")
	verifyDeployment(suite.T(), deployment, d)
}

func (suite *DeploymentTestSuite) TestCreateDeploymentListInstancesFails() {
	suite.environment.EXPECT().GetEnvironment(suite.ctx, environmentName).Return(suite.deploymentEnvironment, nil)

	deployment, err := types.NewDeployment(suite.deploymentEnvironment.DesiredTaskDefinition)
	assert.Nil(suite.T(), err, "Deployment creation failed")

	suite.deploymentEnvironment.Deployments[deployment.ID] = *deployment

	suite.environment.EXPECT().AddDeployment(suite.ctx, *suite.deploymentEnvironment, gomock.Any()).Do(
		func(_ interface{}, _ interface{}, d types.Deployment) {
			verifyDeployment(suite.T(), deployment, &d)
		}).Return(suite.deploymentEnvironment, nil)

	suite.clusterState.EXPECT().ListInstances(suite.deploymentEnvironment.Cluster).Return(nil, errors.New("List instances fails"))
	suite.ecs.EXPECT().StartTask(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
	suite.environment.EXPECT().UpdateDeployment(suite.ctx, gomock.Any(), gomock.Any()).Times(0)

	_, err = suite.deployment.CreateDeployment(suite.ctx, environmentName, suite.deploymentEnvironment.Token)
	assert.Error(suite.T(), err, "Expected an error when listing instances fails")
}

func (suite *DeploymentTestSuite) TestCreateDeploymentListInstancesIsNil() {
	suite.environment.EXPECT().GetEnvironment(suite.ctx, environmentName).Return(suite.deploymentEnvironment, nil)

	deployment, err := types.NewDeployment(suite.deploymentEnvironment.DesiredTaskDefinition)
	assert.Nil(suite.T(), err, "Deployment creation failed")

	suite.deploymentEnvironment.Deployments[deployment.ID] = *deployment

	suite.environment.EXPECT().AddDeployment(suite.ctx, *suite.deploymentEnvironment, gomock.Any()).Do(
		func(_ interface{}, _ interface{}, d types.Deployment) {
			verifyDeployment(suite.T(), deployment, &d)
		}).Return(suite.deploymentEnvironment, nil)

	suite.clusterState.EXPECT().ListInstances(suite.deploymentEnvironment.Cluster).Return(nil, nil)
	suite.ecs.EXPECT().StartTask(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
	suite.environment.EXPECT().UpdateDeployment(suite.ctx, gomock.Any(), gomock.Any()).Times(0)

	_, err = suite.deployment.CreateDeployment(suite.ctx, environmentName, suite.deploymentEnvironment.Token)
	assert.Error(suite.T(), err, "Expected an error when listing instances returns nil")
}

func (suite *DeploymentTestSuite) TestCreateDeploymentListInstancesIsEmpty() {
	suite.environment.EXPECT().GetEnvironment(suite.ctx, environmentName).Return(suite.deploymentEnvironment, nil)

	deployment, err := types.NewDeployment(suite.deploymentEnvironment.DesiredTaskDefinition)
	assert.Nil(suite.T(), err, "Deployment creation failed")

	suite.deploymentEnvironment.Deployments[deployment.ID] = *deployment

	suite.environment.EXPECT().AddDeployment(suite.ctx, *suite.deploymentEnvironment, gomock.Any()).Do(
		func(_ interface{}, _ interface{}, d types.Deployment) {
			verifyDeployment(suite.T(), deployment, &d)
		}).Return(suite.deploymentEnvironment, nil)

	suite.clusterState.EXPECT().ListInstances(suite.deploymentEnvironment.Cluster).Return([]*models.ContainerInstance{}, nil)
	suite.ecs.EXPECT().StartTask(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Times(0)
	suite.environment.EXPECT().UpdateDeployment(suite.ctx, gomock.Any(), gomock.Any()).Times(0)

	_, err = suite.deployment.CreateDeployment(suite.ctx, environmentName, suite.deploymentEnvironment.Token)
	assert.Error(suite.T(), err, "Expected an error when listing instances returns empty")
}

func (suite *DeploymentTestSuite) TestCreateDeploymentStartTasksFails() {
	suite.environment.EXPECT().GetEnvironment(suite.ctx, environmentName).Return(suite.deploymentEnvironment, nil)

	deployment, err := types.NewDeployment(suite.deploymentEnvironment.DesiredTaskDefinition)
	assert.Nil(suite.T(), err, "Deployment creation failed")

	suite.deploymentEnvironment.Deployments[deployment.ID] = *deployment

	suite.environment.EXPECT().AddDeployment(suite.ctx, *suite.deploymentEnvironment, gomock.Any()).Do(
		func(_ interface{}, _ interface{}, d types.Deployment) {
			verifyDeployment(suite.T(), deployment, &d)
		}).Return(suite.deploymentEnvironment, nil)

	suite.clusterState.EXPECT().ListInstances(suite.deploymentEnvironment.Cluster).Return(createContainerInstances(suite.instanceARNs), nil)
	suite.ecs.EXPECT().StartTask(suite.deploymentEnvironment.Cluster, suite.instanceARNs,
		gomock.Any(), suite.deploymentEnvironment.DesiredTaskDefinition).Return(nil, errors.New("Start tasks failed"))
	suite.environment.EXPECT().UpdateDeployment(suite.ctx, gomock.Any(), gomock.Any()).Times(0)

	_, err = suite.deployment.CreateDeployment(suite.ctx, environmentName, suite.deploymentEnvironment.Token)
	assert.Error(suite.T(), err, "Expected an error when start tasks fails")
}

func (suite *DeploymentTestSuite) TestCreateDeploymentUpdateDeploymentFails() {
	suite.environment.EXPECT().GetEnvironment(suite.ctx, environmentName).Return(suite.deploymentEnvironment, nil)

	deployment, err := types.NewDeployment(suite.deploymentEnvironment.DesiredTaskDefinition)
	assert.Nil(suite.T(), err, "Deployment creation failed")

	suite.deploymentEnvironment.Deployments[deployment.ID] = *deployment

	suite.environment.EXPECT().AddDeployment(suite.ctx, *suite.deploymentEnvironment, gomock.Any()).Do(
		func(_ interface{}, _ interface{}, d types.Deployment) {
			verifyDeployment(suite.T(), deployment, &d)
		}).Return(suite.deploymentEnvironment, nil)

	suite.clusterState.EXPECT().ListInstances(suite.deploymentEnvironment.Cluster).Return(createContainerInstances(suite.instanceARNs), nil)

	suite.ecs.EXPECT().StartTask(suite.deploymentEnvironment.Cluster, suite.instanceARNs,
		gomock.Any(), suite.deploymentEnvironment.DesiredTaskDefinition).Return(suite.startTaskOutput, nil)

	updatedDeployment := *deployment
	updatedDeployment.DesiredTaskCount = len(suite.instanceARNs)
	updatedDeployment.Health = types.DeploymentUnhealthy
	updatedDeployment.Status = types.DeploymentInProgress
	updatedDeployment.FailedInstances = suite.startTaskOutput.Failures

	suite.environment.EXPECT().UpdateDeployment(suite.ctx, *suite.deploymentEnvironment, gomock.Any()).Do(
		func(_ interface{}, _ interface{}, d types.Deployment) {
			verifyDeployment(suite.T(), &updatedDeployment, &d)
		}).Return(nil, errors.New("Update deployment failed"))

	_, err = suite.deployment.CreateDeployment(suite.ctx, environmentName, suite.deploymentEnvironment.Token)
	assert.Error(suite.T(), err, "Expected an error when update deployment fails")
}

func (suite *DeploymentTestSuite) TestCreateDeploymentWithoutToken() {
	createDeployment(suite, "")
}

func (suite *DeploymentTestSuite) TestCreateDeploymentWithToken() {
	createDeployment(suite, suite.deploymentEnvironment.Token)
}

func createDeployment(suite *DeploymentTestSuite, token string) {
	suite.environment.EXPECT().GetEnvironment(suite.ctx, environmentName).Return(suite.deploymentEnvironment, nil)

	deployment, err := types.NewDeployment(suite.deploymentEnvironment.DesiredTaskDefinition)
	assert.Nil(suite.T(), err, "Deployment creation failed")

	suite.deploymentEnvironment.Deployments[deployment.ID] = *deployment

	suite.environment.EXPECT().AddDeployment(suite.ctx, *suite.deploymentEnvironment, gomock.Any()).Do(
		func(_ interface{}, _ interface{}, d types.Deployment) {
			verifyDeployment(suite.T(), deployment, &d)
		}).Return(suite.deploymentEnvironment, nil)

	suite.clusterState.EXPECT().ListInstances(suite.deploymentEnvironment.Cluster).Return(createContainerInstances(suite.instanceARNs), nil)

	suite.ecs.EXPECT().StartTask(suite.deploymentEnvironment.Cluster, suite.instanceARNs,
		gomock.Any(), suite.deploymentEnvironment.DesiredTaskDefinition).Return(suite.startTaskOutput, nil)

	updatedDeployment := *deployment
	updatedDeployment.DesiredTaskCount = len(suite.instanceARNs)
	updatedDeployment.Health = types.DeploymentUnhealthy
	updatedDeployment.Status = types.DeploymentInProgress
	updatedDeployment.FailedInstances = suite.startTaskOutput.Failures

	updatedEnvironment := *suite.deploymentEnvironment

	suite.environment.EXPECT().UpdateDeployment(suite.ctx, *suite.deploymentEnvironment, gomock.Any()).Do(
		func(_ interface{}, _ interface{}, d types.Deployment) {
			verifyDeployment(suite.T(), &updatedDeployment, &d)
		}).Return(&updatedEnvironment, nil)

	d, err := suite.deployment.CreateDeployment(suite.ctx, environmentName, token)
	assert.Nil(suite.T(), err, "Unexpected error when creating a deployment")
	verifyDeployment(suite.T(), &updatedDeployment, d)
}

func verifyDeployment(t *testing.T, expected *types.Deployment, actual *types.Deployment) {
	assert.NotEmpty(t, actual.ID, "Deployment ID should not be empty")
	assert.Exactly(t, expected.Status, actual.Status, "Deployment status should match")
	assert.Exactly(t, expected.Health, actual.Health, "Deployment health should match")
	assert.Exactly(t, expected.TaskDefinition, actual.TaskDefinition, "Deployment task definition should match")
	assert.Exactly(t, expected.DesiredTaskCount, actual.DesiredTaskCount, "Deployment desired task count should match")
	assert.NotEmpty(t, actual.StartTime, "Deployment start time should not be empty")
	assert.Exactly(t, expected.EndTime, actual.EndTime, "Deployment end time should match")
}

func (suite *DeploymentTestSuite) TestGetDeploymentEmptyEnvironmentName() {
	_, err := suite.deployment.GetDeployment(suite.ctx, "", deploymentID)
	assert.Error(suite.T(), err, "Expected an error when environment name is empty")
}

func (suite *DeploymentTestSuite) TestGetDeploymentEmptyDeploymentID() {
	_, err := suite.deployment.GetDeployment(suite.ctx, environmentName, "")
	assert.Error(suite.T(), err, "Expected an error when deployment ID is empty")
}

func (suite *DeploymentTestSuite) TestGetDeploymentGetEnvironmentFails() {
	suite.environment.EXPECT().GetEnvironment(suite.ctx, environmentName).
		Return(nil, errors.New("Get environment failed"))

	_, err := suite.deployment.GetDeployment(suite.ctx, environmentName, deploymentID)
	assert.Error(suite.T(), err, "Expected an error when get environment fails")
}

func (suite *DeploymentTestSuite) TestGetDeploymentGetEnvironmentIsNil() {
	suite.environment.EXPECT().GetEnvironment(suite.ctx, environmentName).
		Return(nil, nil)

	_, err := suite.deployment.GetDeployment(suite.ctx, environmentName, deploymentID)
	assert.Error(suite.T(), err, "Expected an error when get environment is nil")
}

func (suite *DeploymentTestSuite) TestGetDeploymentEnvironmentDoesNotHaveDeployments() {
	suite.environment.EXPECT().GetEnvironment(suite.ctx, environmentName).
		Return(suite.deploymentEnvironment, nil)

	d, err := suite.deployment.GetDeployment(suite.ctx, environmentName, deploymentID)
	assert.Nil(suite.T(), err, "Unexpected error when the environment does not have deployments")
	assert.Nil(suite.T(), d, "Expected a nil result when the environment does not have deployments")
}

func (suite *DeploymentTestSuite) TestGetDeploymentEnvironmentDoesNotHaveAMatchingDeployment() {
	deployment, err := types.NewDeployment(suite.deploymentEnvironment.DesiredTaskDefinition)
	assert.Nil(suite.T(), err, "Could not create a new deployment")
	suite.deploymentEnvironment.Deployments[deployment.ID] = *deployment

	suite.environment.EXPECT().GetEnvironment(suite.ctx, environmentName).
		Return(suite.deploymentEnvironment, nil)

	d, err := suite.deployment.GetDeployment(suite.ctx, environmentName, deploymentID)
	assert.Nil(suite.T(), err, "Unexpected error when the environment does not have deployments")
	assert.Nil(suite.T(), d, "Expected a nil result when the environment does not have deployments")
}

func (suite *DeploymentTestSuite) TestGetDeployment() {
	deployment1, err := types.NewDeployment(suite.deploymentEnvironment.DesiredTaskDefinition)
	assert.Nil(suite.T(), err, "Could not create a new deployment")
	suite.deploymentEnvironment.Deployments[deployment1.ID] = *deployment1

	deployment2, err := types.NewDeployment(suite.deploymentEnvironment.DesiredTaskDefinition)
	assert.Nil(suite.T(), err, "Could not create a new deployment")
	suite.deploymentEnvironment.Deployments[deployment2.ID] = *deployment2

	suite.environment.EXPECT().GetEnvironment(suite.ctx, environmentName).
		Return(suite.deploymentEnvironment, nil)

	d, err := suite.deployment.GetDeployment(suite.ctx, environmentName, deployment2.ID)
	assert.Nil(suite.T(), err, "Unexpected error when the environment has multiple deployments")
	assert.Exactly(suite.T(), deployment2, d, "Deployment does not match the one in the environment")
}

func (suite *DeploymentTestSuite) TestListDeploymentsEmptyEnvironmentName() {
	_, err := suite.deployment.ListDeployments(suite.ctx, "")
	assert.Error(suite.T(), err, "Expected an error when environment name is empty")
}

func (suite *DeploymentTestSuite) TestListDeploymentsGetEnvironmentFails() {
	suite.environment.EXPECT().GetEnvironment(suite.ctx, environmentName).
		Return(nil, errors.New("Get environment failed"))

	_, err := suite.deployment.ListDeployments(suite.ctx, environmentName)
	assert.Error(suite.T(), err, "Expected an error when get environment fails")
}

func (suite *DeploymentTestSuite) TestListDeploymentsGetEnvironmentIsNil() {
	suite.environment.EXPECT().GetEnvironment(suite.ctx, environmentName).
		Return(nil, nil)

	_, err := suite.deployment.ListDeployments(suite.ctx, environmentName)
	assert.Error(suite.T(), err, "Expected an error when get environment is nil")
}

func (suite *DeploymentTestSuite) TestListDeploymentsEnvironmentDoesNotHaveDeployments() {
	suite.environment.EXPECT().GetEnvironment(suite.ctx, environmentName).
		Return(suite.deploymentEnvironment, nil)

	d, err := suite.deployment.ListDeployments(suite.ctx, environmentName)
	assert.Nil(suite.T(), err, "Unexpected error when the environment does not have deployments")
	assert.Empty(suite.T(), d, "Expected an empty result when the environment does not have deployments")
}

func (suite *DeploymentTestSuite) TestListDeployments() {
	deployment1, err := types.NewDeployment(suite.deploymentEnvironment.DesiredTaskDefinition)
	assert.Nil(suite.T(), err, "Could not create a new deployment")
	suite.deploymentEnvironment.Deployments[deployment1.ID] = *deployment1

	deployment2, err := types.NewDeployment(suite.deploymentEnvironment.DesiredTaskDefinition)
	assert.Nil(suite.T(), err, "Could not create a new deployment")
	suite.deploymentEnvironment.Deployments[deployment2.ID] = *deployment2

	deployments := map[string]*types.Deployment{
		deployment1.ID: deployment1,
		deployment2.ID: deployment2,
	}

	suite.environment.EXPECT().GetEnvironment(suite.ctx, environmentName).
		Return(suite.deploymentEnvironment, nil)

	actualDeployments, err := suite.deployment.ListDeployments(suite.ctx, environmentName)
	assert.Nil(suite.T(), err, "Unexpected error when the environment listing deployments")
	assert.Exactly(suite.T(), len(deployments), len(actualDeployments), "Deployment length does not match what's expected")

	for _, d := range actualDeployments {
		value, ok := deployments[d.ID]
		if !ok {
			suite.T().Errorf("Actual deployments contains an unexpected item: %v", d)
		} else {
			if !reflect.DeepEqual(*value, d) {
				suite.T().Errorf("Actual deployments item %v does not match expected item %v", d, value)
			}
		}
	}
}

func createContainerInstances(instanceARNs []*string) []*models.ContainerInstance {
	containerInstances := make([]*models.ContainerInstance, 0, len(instanceARNs))
	for _, i := range instanceARNs {
		containerInstance := &models.ContainerInstance{
			ContainerInstanceARN: i,
		}
		containerInstances = append(containerInstances, containerInstance)
	}

	return containerInstances
}
