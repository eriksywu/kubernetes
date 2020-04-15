/*
Copyright 2018 The Kubernetes Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package azure

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/services/compute/mgmt/2019-12-01/compute"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/apimachinery/pkg/util/wait"
	clientset "k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/kubernetes/test/e2e/framework"
	"k8s.io/legacy-cloud-providers/azure"
)

// TODO move these to cmd-line flags
const (
	subscriptionIDEnv    = "SUBSCRIPTION_ID"
	resourceGroupNameEnv = "RESOURCE_GROUP"
	resourceNameEnv      = "AKS_RESOURCE_NAME"
)

const notImplemented = "Not yet implemented for Azure Provider"

func init() {
	framework.Logf("AZURE PROVIDER PACKAGE INITIALIZED")
	framework.RegisterProvider("azure", newProvider)
	framework.RegisterProvider("aks", newProvider)
}

func newProvider() (framework.ProviderInterface, error) {
	framework.Logf("Constructing new Azure Provider")
	if framework.TestContext.CloudConfig.ConfigFile == "" {
		return nil, fmt.Errorf("config-file must be specified for Azure")
	}
	config, err := os.Open(framework.TestContext.CloudConfig.ConfigFile)
	if err != nil {
		framework.Logf("Couldn't open cloud provider configuration %s: %#v",
			framework.TestContext.CloudConfig.ConfigFile, err)
	}
	defer config.Close()
	framework.Logf("New azure cloud client from config file %s", framework.TestContext.CloudConfig.ConfigFile)
	azureCloud, err := azure.NewCloud(config)
	k8sClientSet, err := newK8SClientSet()
	if err != nil {
		return nil, err
	}

	azureCloud.(*azure.Cloud).KubeClient = k8sClientSet

	subscriptionID := os.Getenv(subscriptionIDEnv)
	if subscriptionID == "" {
		return nil, fmt.Errorf("Environment Variable %s not set", subscriptionIDEnv)
	}

	resourceName := os.Getenv(resourceNameEnv)
	if resourceName == "" {
		return nil, fmt.Errorf("Environment Variable %s not set", resourceNameEnv)
	}

	resourceGroupName := os.Getenv(resourceGroupNameEnv)
	if resourceGroupName == "" {
		return nil, fmt.Errorf("Environment Variable %s not set", resourceGroupNameEnv)
	}

	aksClient, err := NewAksClient(subscriptionID)
	if err != nil {
		return nil, err
	}

	return &Provider{
		azureCloud:        azureCloud.(*azure.Cloud),
		aksClient:         aksClient,
		subscriptionID:    subscriptionID,
		resourceName:      resourceName,
		resourceGroupName: resourceGroupName,
	}, err
}

// TODO find out why this isn't instantiated by the legacy azure-cloud-provider builder on line 58
func newK8SClientSet() (clientset.Interface, error) {
	kubeConfig := clientcmd.GetConfigFromFileOrDie(framework.TestContext.KubeConfig)
	restConfig, err := clientcmd.NewDefaultClientConfig(*kubeConfig, &clientcmd.ConfigOverrides{}).ClientConfig()
	if err != nil {
		return nil, err
	}
	k8sClientSet, err := clientset.NewForConfig(restConfig)
	if err != nil {
		return nil, err
	}
	return k8sClientSet, nil
}

//Provider is a structure to handle Azure clouds for e2e testing
type Provider struct {
	framework.NullProvider

	azureCloud *azure.Cloud

	// TODO can I move this into *azure.Cloud?
	aksClient         *AksClient
	subscriptionID    string
	resourceName      string
	resourceGroupName string
}

// GroupSize returns the size of an instance group
// AKS: get vm
func (p *Provider) GroupSize(group string) (int, error) {
	// TODO: need to check if cluster was deployed by aksengine
	framework.Logf("Getting Node List from group = %s", group)

	var nodes *v1.NodeList
	err := wait.PollImmediate(60*time.Second, 10*time.Second, func() (bool, error) {
		var err error
		nodes, err = p.azureCloud.KubeClient.CoreV1().Nodes().List(context.TODO(), metav1.ListOptions{})
		if err != nil {
			return false, err
		}
		return true, nil
	})
	if err != nil {
		return -1, err
	}
	/*
		for _, n := range nodes.Items {
			fmt.Printf("node name: %s \n", n.Name)
			fmt.Printf("node labels: %v \n", n.Labels)
			fmt.Printf("node annotations: %v \n", n.Annotations)
			fmt.Printf("node taints: %v \n", n.Spec.Taints)
		}*/
	return len(nodes.Items), nil
}

// TODO move this over to aks.go
func (p *Provider) ResizeGroup(group string, size int32) error {
	framework.Logf("resizing to size %d", size)
	if p.azureCloud == nil {
		return fmt.Errorf("Azure Cloud not initialized")
	}
	mcList, err := p.aksClient.managedClusterClient.ListByResourceGroup(context.TODO(), p.resourceGroupName)
	if err != nil {
		framework.Logf("error calling MC List api")
		return err
	}
	for mcList.NotDone() {
		list := mcList.Values()
		for _, mc := range list {
			framework.Logf("mc name: %s", *mc.Name)
		}
		mcList.Next()
	}

	framework.Logf("Grabbing Managed Cluster Info from subId = %s, resourcegroup = %s, resource = %s", p.subscriptionID, p.resourceGroupName, p.resourceName)
	mcModel, err := p.aksClient.managedClusterClient.Get(context.TODO(), p.resourceGroupName, p.resourceName)
	if err != nil {
		framework.Logf("error calling MC api")
		return err
	}
	if mcModel.AgentPoolProfiles == nil || len(*mcModel.AgentPoolProfiles) == 0 {
		return fmt.Errorf("No agent pools found")
	}

	// TODO handle multiple agent pools
	agentPoolProfiles := *(mcModel.AgentPoolProfiles)
	for i, p := range agentPoolProfiles {
		framework.Logf("resizing pool %s from size %d to size %d", p.Name, *p.Count, size)
		*agentPoolProfiles[i].Count = size
	}
	// should this share the same context as its returned future object?
	resFuture, err := p.aksClient.managedClusterClient.CreateOrUpdate(context.Background(), p.resourceGroupName, p.resourceName, mcModel)
	if err != nil {
		return err
	}
	// is 1hr too long?
	updateCtx, cancelFn := context.WithTimeout(context.Background(), time.Hour)
	defer cancelFn()
	// wait for update operation to complete
	err = resFuture.WaitForCompletionRef(updateCtx, p.aksClient.managedClusterClient.Client)
	if err != nil {
		return err
	}
	_, err = resFuture.Result(p.aksClient.managedClusterClient)
	if err != nil {
		return err
	}
	var newSize int
	newSize, err = p.GroupSize(group)
	if err != nil {
		return err
	}
	if newSize != int(size)+1 {
		return fmt.Errorf("Kubenetes not updated to new size, actualSize = %d", newSize)
	}

	return nil
}

// DeleteNode deletes a node which is specified as the argument
// Note: The actual node from the agent pool is not deleted
func (p *Provider) DeleteNode(node *v1.Node) error {
	framework.Logf("deleting node = %s", node.Name)
	if p.azureCloud == nil {
		return fmt.Errorf("Azure Cloud not initialized")
	}
	if err := p.azureCloud.KubeClient.CoreV1().Nodes().Delete(context.TODO(), node.Name, metav1.DeleteOptions{}); err != nil {
		return err
	}
	if err := wait.PollImmediate(60*time.Second, 60*time.Second, func() (bool, error) {
		if _, err := p.azureCloud.KubeClient.CoreV1().Nodes().Get(context.TODO(), node.Name, metav1.GetOptions{}); err != nil {
			// node has been deleted - exit with success
			return strings.Contains(err.Error(), string(metav1.StatusReasonNotFound)), nil
		}
		return false, nil
	}); err != nil {
		return err
	}
	return nil

}

// CreatePD creates a persistent volume
func (p *Provider) CreatePD(zone string) (string, error) {
	pdName := fmt.Sprintf("%s-%s", framework.TestContext.Prefix, string(uuid.NewUUID()))

	volumeOptions := &azure.ManagedDiskOptions{
		DiskName:           pdName,
		StorageAccountType: compute.StandardLRS,
		ResourceGroup:      "",
		PVCName:            pdName,
		SizeGB:             1,
		Tags:               nil,
		AvailabilityZone:   zone,
		DiskIOPSReadWrite:  "",
		DiskMBpsReadWrite:  "",
	}
	return p.azureCloud.CreateManagedDisk(volumeOptions)
}

// DeletePD deletes a persistent volume
func (p *Provider) DeletePD(pdName string) error {
	if err := p.azureCloud.DeleteManagedDisk(pdName); err != nil {
		framework.Logf("failed to delete Azure volume %q: %v", pdName, err)
		return err
	}
	return nil
}

// EnableAndDisableInternalLB returns functions for both enabling and disabling internal Load Balancer
func (p *Provider) EnableAndDisableInternalLB() (enable, disable func(svc *v1.Service)) {
	enable = func(svc *v1.Service) {
		svc.ObjectMeta.Annotations = map[string]string{azure.ServiceAnnotationLoadBalancerInternal: "true"}
	}
	disable = func(svc *v1.Service) {
		svc.ObjectMeta.Annotations = map[string]string{azure.ServiceAnnotationLoadBalancerInternal: "false"}
	}
	return
}
