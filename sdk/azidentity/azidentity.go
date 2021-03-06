// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azidentity

import (
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
)

const (
	// AzureChina is a global constant to use in order to access the Azure China cloud.
	AzureChina = "https://login.chinacloudapi.cn/"
	// AzureGermany is a global constant to use in order to access the Azure Germany cloud.
	AzureGermany = "https://login.microsoftonline.de/"
	// AzureGovernment is a global constant to use in order to access the Azure Government cloud.
	AzureGovernment = "https://login.microsoftonline.us/"
	// AzurePublicCloud is a global constant to use in order to access the Azure public cloud.
	AzurePublicCloud = "https://login.microsoftonline.com/"
	// defaultSuffix is a suffix the signals that a string is in scope format
	defaultSuffix = "/.default"
)

var (
	successStatusCodes = [2]int{
		http.StatusOK,      // 200
		http.StatusCreated, // 201
	}
)

type tokenResponse struct {
	token        *azcore.AccessToken
	refreshToken string
}

// AADAuthenticationFailedError is used to unmarshal error responses received from Azure Active Directory.
type AADAuthenticationFailedError struct {
	Message       string `json:"error"`
	Description   string `json:"error_description"`
	Timestamp     string `json:"timestamp"`
	TraceID       string `json:"trace_id"`
	CorrelationID string `json:"correlation_id"`
	URI           string `json:"error_uri"`
	Response      *azcore.Response
}

func (e *AADAuthenticationFailedError) Error() string {
	msg := e.Message
	if len(e.Description) > 0 {
		msg += " " + e.Description
	}
	return msg
}

// AuthenticationFailedError is returned when the authentication request has failed.
type AuthenticationFailedError struct {
	inner error
	msg   string
}

// Unwrap method on AuthenticationFailedError provides access to the inner error if available.
func (e *AuthenticationFailedError) Unwrap() error {
	return e.inner
}

// IsNotRetriable returns true indicating that this is a terminal error.
func (e *AuthenticationFailedError) IsNotRetriable() bool {
	return true
}

func (e *AuthenticationFailedError) Error() string {
	if len(e.msg) == 0 {
		e.msg = e.inner.Error()
	}
	return e.msg
}

func newAADAuthenticationFailedError(resp *azcore.Response) error {
	authFailed := &AADAuthenticationFailedError{Response: resp}
	err := resp.UnmarshalAsJSON(authFailed)
	if err != nil {
		authFailed.Message = resp.Status
		authFailed.Description = "Failed to unmarshal response: " + err.Error()
	}
	return authFailed
}

// CredentialUnavailableError is the error type returned when the conditions required to
// create a credential do not exist or are unavailable.
type CredentialUnavailableError struct {
	// CredentialType holds the name of the credential that is unavailable
	CredentialType string
	// Message contains the reason why the credential is unavailable
	Message string
}

func (e *CredentialUnavailableError) Error() string {
	return e.CredentialType + ": " + e.Message
}

// IsNotRetriable returns true indicating that this is a terminal error.
func (e *CredentialUnavailableError) IsNotRetriable() bool {
	return true
}

// TokenCredentialOptions are used to configure how requests are made to Azure Active Directory.
type TokenCredentialOptions struct {
	// The host of the Azure Active Directory authority. The default is https://login.microsoft.com
	AuthorityHost *url.URL

	// HTTPClient sets the transport for making HTTP requests
	// Leave this as nil to use the default HTTP transport
	HTTPClient azcore.Transport

	// LogOptions configures the built-in request logging policy behavior
	LogOptions azcore.RequestLogOptions

	// Retry configures the built-in retry policy behavior
	Retry *azcore.RetryOptions

	// Telemetry configures the built-in telemetry policy behavior
	Telemetry azcore.TelemetryOptions
}

// setDefaultValues initializes an instance of TokenCredentialOptions with default settings.
func (c *TokenCredentialOptions) setDefaultValues() (*TokenCredentialOptions, error) {
	authorityHost := AzurePublicCloud
	if envAuthorityHost := os.Getenv("AZURE_AUTHORITY_HOST"); envAuthorityHost != "" {
		authorityHost = envAuthorityHost
	}

	if c == nil {
		defaultAuthorityHostURL, err := url.Parse(authorityHost)
		if err != nil {
			return nil, err
		}
		c = &TokenCredentialOptions{AuthorityHost: defaultAuthorityHostURL}
	}

	if c.AuthorityHost == nil {
		defaultAuthorityHostURL, err := url.Parse(authorityHost)
		if err != nil {
			return nil, err
		}
		c.AuthorityHost = defaultAuthorityHostURL
	}

	if len(c.AuthorityHost.Path) == 0 || c.AuthorityHost.Path[len(c.AuthorityHost.Path)-1:] != "/" {
		c.AuthorityHost.Path = c.AuthorityHost.Path + "/"
	}

	return c, nil
}

// newDefaultPipeline creates a pipeline using the specified pipeline options.
func newDefaultPipeline(o TokenCredentialOptions) azcore.Pipeline {
	if o.HTTPClient == nil {
		o.HTTPClient = azcore.DefaultHTTPClientTransport()
	}

	return azcore.NewPipeline(
		o.HTTPClient,
		azcore.NewTelemetryPolicy(o.Telemetry),
		azcore.NewUniqueRequestIDPolicy(),
		azcore.NewRetryPolicy(o.Retry),
		azcore.NewRequestLogPolicy(o.LogOptions))
}

// newDefaultMSIPipeline creates a pipeline using the specified pipeline options needed
// for a Managed Identity, such as a MSI specific retry policy.
func newDefaultMSIPipeline(o ManagedIdentityCredentialOptions) azcore.Pipeline {
	if o.HTTPClient == nil {
		o.HTTPClient = azcore.DefaultHTTPClientTransport()
	}
	var statusCodes []int
	// retry policy for MSI is not end-user configurable
	retryOpts := azcore.RetryOptions{
		MaxRetries: 4,
		RetryDelay: 2 * time.Second,
		TryTimeout: 1 * time.Minute,
		StatusCodes: append(statusCodes,
			// The following status codes are a subset of those found in azcore.StatusCodesForRetry, these are the only ones specifically needed for MSI scenarios
			http.StatusRequestTimeout,      // 408
			http.StatusTooManyRequests,     // 429
			http.StatusInternalServerError, // 500
			http.StatusBadGateway,          // 502
			http.StatusGatewayTimeout,      // 504
			http.StatusNotFound,
			http.StatusGone,
			// all remaining 5xx
			http.StatusNotImplemented,
			http.StatusHTTPVersionNotSupported,
			http.StatusVariantAlsoNegotiates,
			http.StatusInsufficientStorage,
			http.StatusLoopDetected,
			http.StatusNotExtended,
			http.StatusNetworkAuthenticationRequired),
	}

	return azcore.NewPipeline(
		o.HTTPClient,
		azcore.NewTelemetryPolicy(o.Telemetry),
		azcore.NewUniqueRequestIDPolicy(),
		azcore.NewRetryPolicy(&retryOpts),
		azcore.NewRequestLogPolicy(o.LogOptions))
}
