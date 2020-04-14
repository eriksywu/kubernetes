package azure

import (
	"fmt"
	"io/ioutil"

	cs "github.com/Azure/azure-sdk-for-go/services/containerservice/mgmt/2019-08-01/containerservice"
	"github.com/Azure/go-autorest/autorest"
	"github.com/Azure/go-autorest/autorest/adal"
	"github.com/Azure/go-autorest/autorest/azure"
	"k8s.io/kubernetes/test/e2e/framework"
	"k8s.io/legacy-cloud-providers/azure/auth"
	"sigs.k8s.io/yaml"
)

const publicCloud = "AZUREPUBLICCLOUD"

type AksClient struct {
	authConfig           auth.AzureAuthConfig
	managedClusterClient cs.ManagedClustersClient
}

func NewAksClient(subscriptionID string) (*AksClient, error) {
	authConfig, err := getAuthConfigFromCloudConfig()
	if err != nil {
		return nil, err
	}
	mc, err := createMCClient(subscriptionID, authConfig.TenantID, authConfig.AADClientID, authConfig.AADClientSecret)
	if err != nil {
		return nil, err
	}

	return &AksClient{managedClusterClient: *mc,
		authConfig: *authConfig}, nil
}

func createMCClient(subscriptionID string, tenantID string, clientID string, clientSecret string) (*cs.ManagedClustersClient, error) {
	env, err := azure.EnvironmentFromName(publicCloud)
	if err != nil {
		return nil, err
	}

	ouathCfg, err := createOAuthConfig(subscriptionID, tenantID, env)
	if err != nil {
		return nil, err
	}

	armSpt, err := adal.NewServicePrincipalToken(*ouathCfg, clientID, clientSecret, env.ServiceManagementEndpoint)
	if err != nil {
		return nil, err
	}

	authorizer := autorest.NewBearerAuthorizer(armSpt)
	mc := cs.NewManagedClustersClientWithBaseURI(env.ResourceManagerEndpoint, subscriptionID)
	mc.Authorizer = authorizer
	return &mc, nil
}

func getAuthConfigFromCloudConfig() (*auth.AzureAuthConfig, error) {
	authFile := framework.TestContext.CloudConfig.ConfigFile
	content, err := ioutil.ReadFile(authFile)
	if err != nil {
		return nil, fmt.Errorf("error reading config file %v due to error: %v", authFile, err)
	}
	config := &auth.AzureAuthConfig{}
	err = yaml.Unmarshal(content, config)
	if err != nil {
		return nil, fmt.Errorf("error parsing config file %v due to error: %v", authFile, err)
	}
	return config, nil
}

func createOAuthConfig(subscriptionID, tenantID string, env azure.Environment) (*adal.OAuthConfig, error) {
	oauthConfig, err := adal.NewOAuthConfig(env.ActiveDirectoryEndpoint, tenantID)
	if err != nil {
		return nil, err
	}

	return oauthConfig, nil
}
