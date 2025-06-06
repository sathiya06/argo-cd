package v1alpha1

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/argoproj/argo-cd/v3/util/oci"

	"github.com/argoproj/argo-cd/v3/common"
	"github.com/argoproj/argo-cd/v3/util/cert"
	"github.com/argoproj/argo-cd/v3/util/git"
	"github.com/argoproj/argo-cd/v3/util/helm"
	"github.com/argoproj/argo-cd/v3/util/workloadidentity"

	log "github.com/sirupsen/logrus"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// RepoCreds holds the definition for repository credentials
type RepoCreds struct {
	// URL is the URL to which these credentials match
	URL string `json:"url" protobuf:"bytes,1,opt,name=url"`
	// Username for authenticating at the repo server
	Username string `json:"username,omitempty" protobuf:"bytes,2,opt,name=username"`
	// Password for authenticating at the repo server
	Password string `json:"password,omitempty" protobuf:"bytes,3,opt,name=password"`
	// SSHPrivateKey contains the private key data for authenticating at the repo server using SSH (only Git repos)
	SSHPrivateKey string `json:"sshPrivateKey,omitempty" protobuf:"bytes,4,opt,name=sshPrivateKey"`
	// TLSClientCertData specifies the TLS client cert data for authenticating at the repo server
	TLSClientCertData string `json:"tlsClientCertData,omitempty" protobuf:"bytes,5,opt,name=tlsClientCertData"`
	// TLSClientCertKey specifies the TLS client cert key for authenticating at the repo server
	TLSClientCertKey string `json:"tlsClientCertKey,omitempty" protobuf:"bytes,6,opt,name=tlsClientCertKey"`
	// GithubAppPrivateKey specifies the private key PEM data for authentication via GitHub app
	GithubAppPrivateKey string `json:"githubAppPrivateKey,omitempty" protobuf:"bytes,7,opt,name=githubAppPrivateKey"`
	// GithubAppId specifies the Github App ID of the app used to access the repo for GitHub app authentication
	GithubAppId int64 `json:"githubAppID,omitempty" protobuf:"bytes,8,opt,name=githubAppID"`
	// GithubAppInstallationId specifies the ID of the installed GitHub App for GitHub app authentication
	GithubAppInstallationId int64 `json:"githubAppInstallationID,omitempty" protobuf:"bytes,9,opt,name=githubAppInstallationID"`
	// GithubAppEnterpriseBaseURL specifies the GitHub API URL for GitHub app authentication. If empty will default to https://api.github.com
	GitHubAppEnterpriseBaseURL string `json:"githubAppEnterpriseBaseUrl,omitempty" protobuf:"bytes,10,opt,name=githubAppEnterpriseBaseUrl"`
	// EnableOCI specifies whether helm-oci support should be enabled for this repo
	EnableOCI bool `json:"enableOCI,omitempty" protobuf:"bytes,11,opt,name=enableOCI"`
	// Type specifies the type of the repoCreds. Can be either "git" or "helm. "git" is assumed if empty or absent.
	Type string `json:"type,omitempty" protobuf:"bytes,12,opt,name=type"`
	// GCPServiceAccountKey specifies the service account key in JSON format to be used for getting credentials to Google Cloud Source repos
	GCPServiceAccountKey string `json:"gcpServiceAccountKey,omitempty" protobuf:"bytes,13,opt,name=gcpServiceAccountKey"`
	// Proxy specifies the HTTP/HTTPS proxy used to access repos at the repo server
	Proxy string `json:"proxy,omitempty" protobuf:"bytes,19,opt,name=proxy"`
	// ForceHttpBasicAuth specifies whether Argo CD should attempt to force basic auth for HTTP connections
	ForceHttpBasicAuth bool `json:"forceHttpBasicAuth,omitempty" protobuf:"bytes,20,opt,name=forceHttpBasicAuth"` //nolint:revive //FIXME(var-naming)
	// NoProxy specifies a list of targets where the proxy isn't used, applies only in cases where the proxy is applied
	NoProxy string `json:"noProxy,omitempty" protobuf:"bytes,23,opt,name=noProxy"`
	// UseAzureWorkloadIdentity specifies whether to use Azure Workload Identity for authentication
	UseAzureWorkloadIdentity bool `json:"useAzureWorkloadIdentity,omitempty" protobuf:"bytes,24,opt,name=useAzureWorkloadIdentity"`
	// BearerToken contains the bearer token used for Git BitBucket Data Center auth at the repo server
	BearerToken string `json:"bearerToken,omitempty" protobuf:"bytes,25,opt,name=bearerToken"`
	// InsecureOCIForceHttp specifies whether the connection to the repository uses TLS at _all_. If true, no TLS. This flag is applicable for OCI repos only.
	InsecureOCIForceHttp bool `json:"insecureOCIForceHttp,omitempty" protobuf:"bytes,26,opt,name=insecureOCIForceHttp"` //nolint:revive //FIXME(var-naming)
}

// Repository is a repository holding application configurations
type Repository struct {
	// Repo contains the URL to the remote repository
	Repo string `json:"repo" protobuf:"bytes,1,opt,name=repo"`
	// Username contains the user name used for authenticating at the remote repository
	Username string `json:"username,omitempty" protobuf:"bytes,2,opt,name=username"`
	// Password contains the password or PAT used for authenticating at the remote repository
	Password string `json:"password,omitempty" protobuf:"bytes,3,opt,name=password"`
	// SSHPrivateKey contains the PEM data for authenticating at the repo server. Only used with Git repos.
	SSHPrivateKey string `json:"sshPrivateKey,omitempty" protobuf:"bytes,4,opt,name=sshPrivateKey"`
	// ConnectionState contains information about the current state of connection to the repository server
	ConnectionState ConnectionState `json:"connectionState,omitempty" protobuf:"bytes,5,opt,name=connectionState"`
	// InsecureIgnoreHostKey should not be used anymore, Insecure is favoured
	// Used only for Git repos
	InsecureIgnoreHostKey bool `json:"insecureIgnoreHostKey,omitempty" protobuf:"bytes,6,opt,name=insecureIgnoreHostKey"`
	// Insecure specifies whether the connection to the repository ignores any errors when verifying TLS certificates or SSH host keys
	Insecure bool `json:"insecure,omitempty" protobuf:"bytes,7,opt,name=insecure"`
	// EnableLFS specifies whether git-lfs support should be enabled for this repo. Only valid for Git repositories.
	EnableLFS bool `json:"enableLfs,omitempty" protobuf:"bytes,8,opt,name=enableLfs"`
	// TLSClientCertData contains a certificate in PEM format for authenticating at the repo server
	TLSClientCertData string `json:"tlsClientCertData,omitempty" protobuf:"bytes,9,opt,name=tlsClientCertData"`
	// TLSClientCertKey contains a private key in PEM format for authenticating at the repo server
	TLSClientCertKey string `json:"tlsClientCertKey,omitempty" protobuf:"bytes,10,opt,name=tlsClientCertKey"`
	// Type specifies the type of the repo. Can be either "git" or "helm. "git" is assumed if empty or absent.
	Type string `json:"type,omitempty" protobuf:"bytes,11,opt,name=type"`
	// Name specifies a name to be used for this repo. Only used with Helm repos
	Name string `json:"name,omitempty" protobuf:"bytes,12,opt,name=name"`
	// Whether credentials were inherited from a credential set
	InheritedCreds bool `json:"inheritedCreds,omitempty" protobuf:"bytes,13,opt,name=inheritedCreds"`
	// EnableOCI specifies whether helm-oci support should be enabled for this repo
	EnableOCI bool `json:"enableOCI,omitempty" protobuf:"bytes,14,opt,name=enableOCI"`
	// Github App Private Key PEM data
	GithubAppPrivateKey string `json:"githubAppPrivateKey,omitempty" protobuf:"bytes,15,opt,name=githubAppPrivateKey"`
	// GithubAppId specifies the ID of the GitHub app used to access the repo
	GithubAppId int64 `json:"githubAppID,omitempty" protobuf:"bytes,16,opt,name=githubAppID"`
	// GithubAppInstallationId specifies the installation ID of the GitHub App used to access the repo
	GithubAppInstallationId int64 `json:"githubAppInstallationID,omitempty" protobuf:"bytes,17,opt,name=githubAppInstallationID"`
	// GithubAppEnterpriseBaseURL specifies the base URL of GitHub Enterprise installation. If empty will default to https://api.github.com
	GitHubAppEnterpriseBaseURL string `json:"githubAppEnterpriseBaseUrl,omitempty" protobuf:"bytes,18,opt,name=githubAppEnterpriseBaseUrl"`
	// Proxy specifies the HTTP/HTTPS proxy used to access the repo
	Proxy string `json:"proxy,omitempty" protobuf:"bytes,19,opt,name=proxy"`
	// Reference between project and repository that allows it to be automatically added as an item inside SourceRepos project entity
	Project string `json:"project,omitempty" protobuf:"bytes,20,opt,name=project"`
	// GCPServiceAccountKey specifies the service account key in JSON format to be used for getting credentials to Google Cloud Source repos
	GCPServiceAccountKey string `json:"gcpServiceAccountKey,omitempty" protobuf:"bytes,21,opt,name=gcpServiceAccountKey"`
	// ForceHttpBasicAuth specifies whether Argo CD should attempt to force basic auth for HTTP connections
	ForceHttpBasicAuth bool `json:"forceHttpBasicAuth,omitempty" protobuf:"bytes,22,opt,name=forceHttpBasicAuth"` //nolint:revive //FIXME(var-naming)
	// NoProxy specifies a list of targets where the proxy isn't used, applies only in cases where the proxy is applied
	NoProxy string `json:"noProxy,omitempty" protobuf:"bytes,23,opt,name=noProxy"`
	// UseAzureWorkloadIdentity specifies whether to use Azure Workload Identity for authentication
	UseAzureWorkloadIdentity bool `json:"useAzureWorkloadIdentity,omitempty" protobuf:"bytes,24,opt,name=useAzureWorkloadIdentity"`
	// BearerToken contains the bearer token used for Git BitBucket Data Center auth at the repo server
	BearerToken string `json:"bearerToken,omitempty" protobuf:"bytes,25,opt,name=bearerToken"`
	// InsecureOCIForceHttp specifies whether the connection to the repository uses TLS at _all_. If true, no TLS. This flag is applicable for OCI repos only.
	InsecureOCIForceHttp bool `json:"insecureOCIForceHttp,omitempty" protobuf:"bytes,26,opt,name=insecureOCIForceHttp"` //nolint:revive //FIXME(var-naming)
}

// IsInsecure returns true if the repository has been configured to skip server verification or set to HTTP only
func (repo *Repository) IsInsecure() bool {
	return repo.InsecureIgnoreHostKey || repo.Insecure || repo.InsecureOCIForceHttp
}

// IsLFSEnabled returns true if LFS support is enabled on repository
func (repo *Repository) IsLFSEnabled() bool {
	return repo.EnableLFS
}

// HasCredentials returns true when the repository has been configured with any credentials
func (repo *Repository) HasCredentials() bool {
	return repo.Username != "" || repo.Password != "" || repo.BearerToken != "" || repo.SSHPrivateKey != "" || repo.TLSClientCertData != "" || repo.GithubAppPrivateKey != "" || repo.UseAzureWorkloadIdentity
}

// CopyCredentialsFromRepo copies all credential information from source repository to receiving repository
func (repo *Repository) CopyCredentialsFromRepo(source *Repository) {
	if source != nil {
		if repo.Username == "" {
			repo.Username = source.Username
		}
		if repo.Password == "" {
			repo.Password = source.Password
		}
		if repo.BearerToken == "" {
			repo.BearerToken = source.BearerToken
		}
		if repo.SSHPrivateKey == "" {
			repo.SSHPrivateKey = source.SSHPrivateKey
		}
		if repo.TLSClientCertData == "" {
			repo.TLSClientCertData = source.TLSClientCertData
		}
		if repo.TLSClientCertKey == "" {
			repo.TLSClientCertKey = source.TLSClientCertKey
		}
		if repo.GithubAppPrivateKey == "" {
			repo.GithubAppPrivateKey = source.GithubAppPrivateKey
		}
		if repo.GithubAppId == 0 {
			repo.GithubAppId = source.GithubAppId
		}
		if repo.GithubAppInstallationId == 0 {
			repo.GithubAppInstallationId = source.GithubAppInstallationId
		}
		if repo.GitHubAppEnterpriseBaseURL == "" {
			repo.GitHubAppEnterpriseBaseURL = source.GitHubAppEnterpriseBaseURL
		}
		if repo.GCPServiceAccountKey == "" {
			repo.GCPServiceAccountKey = source.GCPServiceAccountKey
		}
		repo.InsecureOCIForceHttp = source.InsecureOCIForceHttp
		repo.ForceHttpBasicAuth = source.ForceHttpBasicAuth
		repo.UseAzureWorkloadIdentity = source.UseAzureWorkloadIdentity
	}
}

// CopyCredentialsFrom copies credentials from given credential template to receiving repository
func (repo *Repository) CopyCredentialsFrom(source *RepoCreds) {
	if source != nil {
		if repo.Username == "" {
			repo.Username = source.Username
		}
		if repo.Password == "" {
			repo.Password = source.Password
		}
		if repo.BearerToken == "" {
			repo.BearerToken = source.BearerToken
		}
		if repo.SSHPrivateKey == "" {
			repo.SSHPrivateKey = source.SSHPrivateKey
		}
		if repo.TLSClientCertData == "" {
			repo.TLSClientCertData = source.TLSClientCertData
		}
		if repo.TLSClientCertKey == "" {
			repo.TLSClientCertKey = source.TLSClientCertKey
		}
		if repo.GithubAppPrivateKey == "" {
			repo.GithubAppPrivateKey = source.GithubAppPrivateKey
		}
		if repo.GithubAppId == 0 {
			repo.GithubAppId = source.GithubAppId
		}
		if repo.GithubAppInstallationId == 0 {
			repo.GithubAppInstallationId = source.GithubAppInstallationId
		}
		if repo.GitHubAppEnterpriseBaseURL == "" {
			repo.GitHubAppEnterpriseBaseURL = source.GitHubAppEnterpriseBaseURL
		}
		if repo.GCPServiceAccountKey == "" {
			repo.GCPServiceAccountKey = source.GCPServiceAccountKey
		}
		if repo.Proxy == "" {
			repo.Proxy = source.Proxy
		}
		if repo.NoProxy == "" {
			repo.NoProxy = source.NoProxy
		}
		if repo.Type == "" {
			repo.Type = source.Type
		}

		repo.EnableOCI = source.EnableOCI
		repo.InsecureOCIForceHttp = source.InsecureOCIForceHttp
		repo.ForceHttpBasicAuth = source.ForceHttpBasicAuth
		repo.UseAzureWorkloadIdentity = source.UseAzureWorkloadIdentity
	}
}

// GetGitCreds returns the credentials from a repository configuration used to authenticate at a Git repository
func (repo *Repository) GetGitCreds(store git.CredsStore) git.Creds {
	if repo == nil {
		return git.NopCreds{}
	}
	if repo.Password != "" || repo.BearerToken != "" {
		return git.NewHTTPSCreds(repo.Username, repo.Password, repo.BearerToken, repo.TLSClientCertData, repo.TLSClientCertKey, repo.IsInsecure(), store, repo.ForceHttpBasicAuth)
	}
	if repo.SSHPrivateKey != "" {
		return git.NewSSHCreds(repo.SSHPrivateKey, getCAPath(repo.Repo), repo.IsInsecure(), repo.Proxy)
	}
	if repo.GithubAppPrivateKey != "" && repo.GithubAppId != 0 && repo.GithubAppInstallationId != 0 {
		return git.NewGitHubAppCreds(repo.GithubAppId, repo.GithubAppInstallationId, repo.GithubAppPrivateKey, repo.GitHubAppEnterpriseBaseURL, repo.TLSClientCertData, repo.TLSClientCertKey, repo.IsInsecure(), repo.Proxy, repo.NoProxy, store)
	}
	if repo.GCPServiceAccountKey != "" {
		return git.NewGoogleCloudCreds(repo.GCPServiceAccountKey, store)
	}
	if repo.UseAzureWorkloadIdentity {
		return git.NewAzureWorkloadIdentityCreds(store, workloadidentity.NewWorkloadIdentityTokenProvider())
	}
	return git.NopCreds{}
}

// GetHelmCreds returns the credentials from a repository configuration used to authenticate a Helm repository
func (repo *Repository) GetHelmCreds() helm.Creds {
	if repo.UseAzureWorkloadIdentity {
		return helm.NewAzureWorkloadIdentityCreds(
			repo.Repo,
			getCAPath(repo.Repo),
			[]byte(repo.TLSClientCertData),
			[]byte(repo.TLSClientCertKey),
			repo.Insecure,
			workloadidentity.NewWorkloadIdentityTokenProvider(),
		)
	}

	return helm.HelmCreds{
		Username:           repo.Username,
		Password:           repo.Password,
		CAPath:             getCAPath(repo.Repo),
		CertData:           []byte(repo.TLSClientCertData),
		KeyData:            []byte(repo.TLSClientCertKey),
		InsecureSkipVerify: repo.Insecure,
	}
}

// GetOCICreds returns the credentials from a repository configuration used to authenticate an OCI repository
func (repo *Repository) GetOCICreds() oci.Creds {
	return oci.Creds{
		Username:           repo.Username,
		Password:           repo.Password,
		CAPath:             getCAPath(repo.Repo),
		CertData:           []byte(repo.TLSClientCertData),
		KeyData:            []byte(repo.TLSClientCertKey),
		InsecureSkipVerify: repo.Insecure,
		InsecureHTTPOnly:   repo.InsecureOCIForceHttp,
	}
}

func getCAPath(repoURL string) string {
	// For git ssh protocol url without ssh://, url.Parse() will fail to parse.
	// However, no warn log is output since ssh scheme url is a possible format.
	if ok, _ := git.IsSSHURL(repoURL); ok {
		return ""
	}

	hostname := ""
	var parsedURL *url.URL
	var err error
	// Without schema in url, url.Parse() treats the url as differently
	// and may incorrectly parses the hostname if url contains a path or port.
	// To ensure proper parsing, prepend a dummy schema.
	if !strings.Contains(repoURL, "://") {
		parsedURL, err = url.Parse("protocol://" + repoURL)
	} else {
		parsedURL, err = url.Parse(repoURL)
	}
	if err != nil {
		log.Warnf("Could not parse repo URL '%s': %v", repoURL, err)
		return ""
	}

	hostname = parsedURL.Hostname()
	if hostname == "" {
		log.Warnf("Could not get hostname for repository '%s'", repoURL)
		return ""
	}

	caPath, err := cert.GetCertBundlePathForRepository(hostname)
	if err != nil {
		log.Warnf("Could not get cert bundle path for repository '%s': %v", repoURL, err)
		return ""
	}

	return caPath
}

// CopySettingsFrom copies all repository settings from source to receiver
func (repo *Repository) CopySettingsFrom(source *Repository) {
	if source != nil {
		repo.EnableLFS = source.EnableLFS
		repo.InsecureIgnoreHostKey = source.InsecureIgnoreHostKey
		repo.Insecure = source.Insecure
		repo.InheritedCreds = source.InheritedCreds
	}
}

// StringForLogging gets a string representation of the Repository which is safe to log or return to the user.
func (repo *Repository) StringForLogging() string {
	if repo == nil {
		return ""
	}
	return fmt.Sprintf("&Repository{Repo: %q, Type: %q, Name: %q, Project: %q}", repo.Repo, repo.Type, repo.Name, repo.Project)
}

// Sanitized returns a copy of the Repository with sensitive information removed.
func (repo *Repository) Sanitized() *Repository {
	return &Repository{
		Repo:                       repo.Repo,
		Type:                       repo.Type,
		Name:                       repo.Name,
		Username:                   repo.Username,
		Insecure:                   repo.IsInsecure(),
		EnableLFS:                  repo.EnableLFS,
		EnableOCI:                  repo.EnableOCI,
		Proxy:                      repo.Proxy,
		NoProxy:                    repo.NoProxy,
		Project:                    repo.Project,
		ForceHttpBasicAuth:         repo.ForceHttpBasicAuth,
		InheritedCreds:             repo.InheritedCreds,
		GithubAppId:                repo.GithubAppId,
		GithubAppInstallationId:    repo.GithubAppInstallationId,
		GitHubAppEnterpriseBaseURL: repo.GitHubAppEnterpriseBaseURL,
		UseAzureWorkloadIdentity:   repo.UseAzureWorkloadIdentity,
	}
}

func (repo *Repository) Normalize() *Repository {
	if repo.Type == "" {
		repo.Type = common.DefaultRepoType
	}
	return repo
}

// Repositories defines a list of Repository configurations
type Repositories []*Repository

// Filter returns a list of repositories, which only contain items matched by the supplied predicate method
func (r Repositories) Filter(predicate func(r *Repository) bool) Repositories {
	var res Repositories
	for i := range r {
		repo := r[i]
		if predicate(repo) {
			res = append(res, repo)
		}
	}
	return res
}

// RepositoryList is a collection of Repositories.
type RepositoryList struct {
	metav1.ListMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`
	Items           Repositories `json:"items" protobuf:"bytes,2,rep,name=items"`
}

// RepositoryList is a collection of Repositories.
type RepoCredsList struct {
	metav1.ListMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`
	Items           []RepoCreds `json:"items" protobuf:"bytes,2,rep,name=items"`
}

// A RepositoryCertificate is either SSH known hosts entry or TLS certificate
type RepositoryCertificate struct {
	// ServerName specifies the DNS name of the server this certificate is intended for
	ServerName string `json:"serverName" protobuf:"bytes,1,opt,name=serverName"`
	// CertType specifies the type of the certificate - currently one of "https" or "ssh"
	CertType string `json:"certType" protobuf:"bytes,2,opt,name=certType"`
	// CertSubType specifies the sub type of the cert, i.e. "ssh-rsa"
	CertSubType string `json:"certSubType" protobuf:"bytes,3,opt,name=certSubType"`
	// CertData contains the actual certificate data, dependent on the certificate type
	CertData []byte `json:"certData" protobuf:"bytes,4,opt,name=certData"`
	// CertInfo will hold additional certificate info, depdendent on the certificate type (e.g. SSH fingerprint, X509 CommonName)
	CertInfo string `json:"certInfo" protobuf:"bytes,5,opt,name=certInfo"`
}

// RepositoryCertificateList is a collection of RepositoryCertificates
type RepositoryCertificateList struct {
	metav1.ListMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`
	// List of certificates to be processed
	Items []RepositoryCertificate `json:"items" protobuf:"bytes,2,rep,name=items"`
}

// GnuPGPublicKey is a representation of a GnuPG public key
type GnuPGPublicKey struct {
	// KeyID specifies the key ID, in hexadecimal string format
	KeyID string `json:"keyID" protobuf:"bytes,1,opt,name=keyID"`
	// Fingerprint is the fingerprint of the key
	Fingerprint string `json:"fingerprint,omitempty" protobuf:"bytes,2,opt,name=fingerprint"`
	// Owner holds the owner identification, e.g. a name and e-mail address
	Owner string `json:"owner,omitempty" protobuf:"bytes,3,opt,name=owner"`
	// Trust holds the level of trust assigned to this key
	Trust string `json:"trust,omitempty" protobuf:"bytes,4,opt,name=trust"`
	// SubType holds the key's subtype (e.g. rsa4096)
	SubType string `json:"subType,omitempty" protobuf:"bytes,5,opt,name=subType"`
	// KeyData holds the raw key data, in base64 encoded format
	KeyData string `json:"keyData,omitempty" protobuf:"bytes,6,opt,name=keyData"`
}

// GnuPGPublicKeyList is a collection of GnuPGPublicKey objects
type GnuPGPublicKeyList struct {
	metav1.ListMeta `json:"metadata,omitempty" protobuf:"bytes,1,opt,name=metadata"`
	Items           []GnuPGPublicKey `json:"items" protobuf:"bytes,2,rep,name=items"`
}
