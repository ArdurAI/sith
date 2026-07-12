// SPDX-License-Identifier: Apache-2.0

package hubauth

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultAWSSTSTimeout       = 5 * time.Second
	maximumAWSSTSTimeout       = 30 * time.Second
	maximumAWSSTSPresignExpiry = time.Minute
	maximumAWSSTSResponseBytes = 16 * 1024
	maximumAWSSTSPresignBytes  = 16 * 1024
	awsSTSAudienceHeader       = "X-Sith-Audience"
	awsSTSXMLNamespace         = "https://sts.amazonaws.com/doc/2011-06-15/"
)

// AWSSTSVerifierConfig pins one regional AWS STS identity-proof profile.
// Endpoint must be an explicit regional endpoint in the commercial, GovCloud, or China partition.
type AWSSTSVerifierConfig struct {
	Endpoint string
	Audience string
	Timeout  time.Duration
	Now      func() time.Time
}

// AWSSTSVerifier validates an audience-bound, short-lived SigV4 GetCallerIdentity proof.
// It never accepts caller-supplied endpoints, headers, or long-lived IAM-user identities.
type AWSSTSVerifier struct {
	endpoint  *url.URL
	region    string
	partition string
	audience  string
	client    *http.Client
	now       func() time.Time
}

type awsSTSPresignedProof struct {
	url       *url.URL
	issuedAt  time.Time
	expiresAt time.Time
}

type awsSTSGetCallerIdentityResponse struct {
	XMLName xml.Name `xml:"GetCallerIdentityResponse"`
	Result  struct {
		Account string `xml:"Account"`
		ARN     string `xml:"Arn"`
		UserID  string `xml:"UserId"`
	} `xml:"GetCallerIdentityResult"`
}

// NewAWSSTSVerifier constructs a production verifier with an isolated HTTPS client.
func NewAWSSTSVerifier(config AWSSTSVerifierConfig) (*AWSSTSVerifier, error) {
	return newAWSSTSVerifier(config, nil)
}

func newAWSSTSVerifier(config AWSSTSVerifierConfig, client *http.Client) (*AWSSTSVerifier, error) {
	endpoint, region, partition, err := validateAWSSTSEndpoint(config.Endpoint)
	if err != nil {
		return nil, err
	}
	if validateCloudIdentityValue("AWS STS audience", config.Audience, maxCloudAudienceBytes) != nil {
		return nil, fmt.Errorf("construct AWS STS verifier: audience is invalid")
	}
	if config.Timeout == 0 {
		config.Timeout = defaultAWSSTSTimeout
	}
	if config.Timeout < time.Second || config.Timeout > maximumAWSSTSTimeout {
		return nil, fmt.Errorf("construct AWS STS verifier: timeout must be between one and 30 seconds")
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	if client == nil {
		client = newAWSSTSHTTPClient(config.Timeout)
	} else {
		client = cloneAWSSTSHTTPClient(client, config.Timeout)
	}
	return &AWSSTSVerifier{
		endpoint: endpoint, region: region, partition: partition, audience: config.Audience,
		client: client, now: config.Now,
	}, nil
}

// Provider implements CloudProofVerifier.
func (verifier *AWSSTSVerifier) Provider() CloudProvider {
	return CloudProviderAWS
}

// Verify forwards one tightly constrained presigned request to the configured STS endpoint.
func (verifier *AWSSTSVerifier) Verify(ctx context.Context, rawProof string) (VerifiedCloudPrincipal, error) {
	if verifier == nil || verifier.endpoint == nil || verifier.client == nil || verifier.now == nil || ctx == nil || ctx.Err() != nil {
		return VerifiedCloudPrincipal{}, ErrInvalidCloudProof
	}
	proof, err := verifier.parseProof(rawProof)
	if err != nil {
		return VerifiedCloudPrincipal{}, ErrInvalidCloudProof
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, proof.url.String(), nil)
	if err != nil {
		return VerifiedCloudPrincipal{}, ErrInvalidCloudProof
	}
	request.Header.Set(awsSTSAudienceHeader, verifier.audience)
	response, err := verifier.client.Do(request)
	if err != nil {
		return VerifiedCloudPrincipal{}, ErrInvalidCloudProof
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return VerifiedCloudPrincipal{}, ErrInvalidCloudProof
	}
	identityResponse, err := decodeAWSSTSIdentityResponse(response.Body)
	if err != nil {
		return VerifiedCloudPrincipal{}, ErrInvalidCloudProof
	}
	identity, err := normalizeAWSSTSIdentity(identityResponse.Result.Account, identityResponse.Result.ARN, identityResponse.Result.UserID, verifier.partition)
	if err != nil {
		return VerifiedCloudPrincipal{}, ErrInvalidCloudProof
	}
	return VerifiedCloudPrincipal{
		Identity: identity, Audience: verifier.audience, IssuedAt: proof.issuedAt, ExpiresAt: proof.expiresAt,
	}, nil
}

func newAWSSTSHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:               nil,
			DisableCompression:  true,
			MaxIdleConnsPerHost: 2,
			IdleConnTimeout:     time.Minute,
			TLSClientConfig: &tls.Config{ // #nosec G402 -- TLS 1.2 is the explicit compatibility floor.
				MinVersion: tls.VersionTLS12,
			},
		},
		Timeout: timeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

func cloneAWSSTSHTTPClient(client *http.Client, timeout time.Duration) *http.Client {
	cloned := *client
	cloned.Timeout = timeout
	cloned.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	return &cloned
}

func (verifier *AWSSTSVerifier) parseProof(rawProof string) (awsSTSPresignedProof, error) {
	if rawProof == "" || len(rawProof) > maxCloudProofBytes || strings.TrimSpace(rawProof) != rawProof {
		return awsSTSPresignedProof{}, fmt.Errorf("AWS STS proof is invalid")
	}
	encodedURL, err := base64.RawURLEncoding.DecodeString(rawProof)
	if err != nil || len(encodedURL) == 0 || len(encodedURL) > maximumAWSSTSPresignBytes {
		return awsSTSPresignedProof{}, fmt.Errorf("AWS STS proof is invalid")
	}
	proofURL, err := url.ParseRequestURI(string(encodedURL))
	if err != nil || proofURL.Scheme != "https" || proofURL.User != nil || proofURL.Fragment != "" ||
		proofURL.Host != verifier.endpoint.Host || proofURL.Path != "/" || proofURL.RawPath != "" || proofURL.ForceQuery || proofURL.RawQuery == "" {
		return awsSTSPresignedProof{}, fmt.Errorf("AWS STS proof is invalid")
	}
	query, err := url.ParseQuery(proofURL.RawQuery)
	if err != nil || !validAWSSTSProofQuery(query) {
		return awsSTSPresignedProof{}, fmt.Errorf("AWS STS proof is invalid")
	}
	credential := strings.Split(query.Get("X-Amz-Credential"), "/")
	if len(credential) != 5 || !validAWSAccessKeyID(credential[0]) || credential[2] != verifier.region ||
		credential[3] != "sts" || credential[4] != "aws4_request" {
		return awsSTSPresignedProof{}, fmt.Errorf("AWS STS proof is invalid")
	}
	issuedAt, err := time.Parse("20060102T150405Z", query.Get("X-Amz-Date"))
	if err != nil || credential[1] != query.Get("X-Amz-Date")[:8] || issuedAt.After(verifier.now().UTC()) {
		return awsSTSPresignedProof{}, fmt.Errorf("AWS STS proof is invalid")
	}
	expiresIn, err := strconv.Atoi(query.Get("X-Amz-Expires"))
	if err != nil || expiresIn < 1 || expiresIn > int(maximumAWSSTSPresignExpiry/time.Second) {
		return awsSTSPresignedProof{}, fmt.Errorf("AWS STS proof is invalid")
	}
	expiresAt := issuedAt.Add(time.Duration(expiresIn) * time.Second)
	if !expiresAt.After(verifier.now().UTC()) {
		return awsSTSPresignedProof{}, fmt.Errorf("AWS STS proof is invalid")
	}
	return awsSTSPresignedProof{url: proofURL, issuedAt: issuedAt, expiresAt: expiresAt}, nil
}

func validAWSSTSProofQuery(query url.Values) bool {
	allowed := map[string]bool{
		"Action": true, "Version": true, "X-Amz-Algorithm": true, "X-Amz-Credential": true,
		"X-Amz-Date": true, "X-Amz-Expires": true, "X-Amz-SignedHeaders": true,
		"X-Amz-Signature": true, "X-Amz-Security-Token": true,
	}
	for key, values := range query {
		if !allowed[key] || len(values) != 1 || values[0] == "" {
			return false
		}
	}
	for _, key := range []string{"Action", "Version", "X-Amz-Algorithm", "X-Amz-Credential", "X-Amz-Date", "X-Amz-Expires", "X-Amz-SignedHeaders", "X-Amz-Signature"} {
		if len(query[key]) != 1 {
			return false
		}
	}
	if query.Get("Action") != "GetCallerIdentity" || query.Get("Version") != "2011-06-15" ||
		query.Get("X-Amz-Algorithm") != "AWS4-HMAC-SHA256" || query.Get("X-Amz-SignedHeaders") != "host;x-sith-audience" {
		return false
	}
	signature := query.Get("X-Amz-Signature")
	if len(signature) != 64 || signature != strings.ToLower(signature) {
		return false
	}
	_, err := hex.DecodeString(signature)
	return err == nil
}

func validateAWSSTSEndpoint(rawEndpoint string) (*url.URL, string, string, error) {
	endpoint, err := url.ParseRequestURI(rawEndpoint)
	if err != nil || endpoint.Scheme != "https" || endpoint.User != nil || endpoint.Port() != "" || endpoint.Path != "" && endpoint.Path != "/" ||
		endpoint.RawPath != "" || endpoint.RawQuery != "" || endpoint.Fragment != "" || endpoint.Hostname() == "" ||
		endpoint.Host != strings.ToLower(endpoint.Host) {
		return nil, "", "", fmt.Errorf("construct AWS STS verifier: endpoint must be a lowercase regional HTTPS STS URL")
	}
	host := endpoint.Hostname()
	var region, partition string
	switch {
	case strings.HasSuffix(host, ".amazonaws.com.cn"):
		region = strings.TrimSuffix(strings.TrimPrefix(host, "sts."), ".amazonaws.com.cn")
		partition = "aws-cn"
		if !strings.HasPrefix(region, "cn-") {
			return nil, "", "", fmt.Errorf("construct AWS STS verifier: China endpoint region is invalid")
		}
	case strings.HasSuffix(host, ".amazonaws.com"):
		region = strings.TrimSuffix(strings.TrimPrefix(host, "sts."), ".amazonaws.com")
		if strings.HasPrefix(region, "us-gov-") {
			partition = "aws-us-gov"
		} else if strings.HasPrefix(region, "cn-") {
			return nil, "", "", fmt.Errorf("construct AWS STS verifier: China endpoints must use the China partition suffix")
		} else {
			partition = "aws"
		}
	default:
		return nil, "", "", fmt.Errorf("construct AWS STS verifier: endpoint partition is unsupported")
	}
	if region == "" || host != "sts."+region+partitionSuffix(partition) || !validAWSRegion(region) {
		return nil, "", "", fmt.Errorf("construct AWS STS verifier: endpoint must be an explicit regional STS host")
	}
	endpoint.Path = "/"
	return endpoint, region, partition, nil
}

func partitionSuffix(partition string) string {
	if partition == "aws-cn" {
		return ".amazonaws.com.cn"
	}
	return ".amazonaws.com"
}

func validAWSRegion(region string) bool {
	parts := strings.Split(region, "-")
	if len(parts) < 3 || parts[len(parts)-1] == "" {
		return false
	}
	for index, part := range parts {
		if part == "" || (index == len(parts)-1 && !allASCIIDigits(part)) || (index != len(parts)-1 && !allLowercaseAlpha(part)) {
			return false
		}
	}
	return true
}

func validAWSAccessKeyID(value string) bool {
	return len(value) >= 16 && len(value) <= 128 && allUppercaseAlphaNumeric(value)
}

func normalizeAWSSTSIdentity(account, arn, userID, partition string) (CloudIdentity, error) {
	if len(account) != 12 || !allASCIIDigits(account) {
		return CloudIdentity{}, fmt.Errorf("AWS STS account is invalid")
	}
	arnParts := strings.SplitN(arn, ":", 6)
	if len(arnParts) != 6 || arnParts[0] != "arn" || arnParts[1] != partition || arnParts[2] != "sts" || arnParts[3] != "" ||
		arnParts[4] != account || !strings.HasPrefix(arnParts[5], "assumed-role/") || strings.Count(arnParts[5], "/") < 2 {
		return CloudIdentity{}, fmt.Errorf("AWS STS principal is not a short-lived assumed role")
	}
	roleID, sessionName, found := strings.Cut(userID, ":")
	if validateCloudIdentityValue("AWS STS ARN resource", arnParts[5], maxCloudIdentityBytes) != nil || !found ||
		!strings.HasPrefix(roleID, "ARO") || len(roleID) < 16 || !allUppercaseAlphaNumeric(roleID) || !validAWSRoleSessionName(sessionName) {
		return CloudIdentity{}, fmt.Errorf("AWS STS principal is invalid")
	}
	identity := CloudIdentity{Provider: CloudProviderAWS, Realm: account, Subject: "role-id:" + roleID}
	if err := identity.Validate(); err != nil {
		return CloudIdentity{}, err
	}
	return identity, nil
}

func decodeAWSSTSIdentityResponse(body io.Reader) (awsSTSGetCallerIdentityResponse, error) {
	limitedBody := io.LimitReader(body, maximumAWSSTSResponseBytes+1)
	document, err := io.ReadAll(limitedBody)
	if err != nil || len(document) == 0 || len(document) > maximumAWSSTSResponseBytes {
		return awsSTSGetCallerIdentityResponse{}, fmt.Errorf("AWS STS response is invalid")
	}
	decoder := xml.NewDecoder(strings.NewReader(string(document)))
	decoder.Strict = true
	var response awsSTSGetCallerIdentityResponse
	if err := decoder.Decode(&response); err != nil || response.XMLName.Space != awsSTSXMLNamespace || response.XMLName.Local != "GetCallerIdentityResponse" {
		return awsSTSGetCallerIdentityResponse{}, fmt.Errorf("AWS STS response is invalid")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return awsSTSGetCallerIdentityResponse{}, fmt.Errorf("AWS STS response is invalid")
	}
	return response, nil
}

func allASCIIDigits(value string) bool {
	if value == "" {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func allLowercaseAlpha(value string) bool {
	for _, character := range value {
		if character < 'a' || character > 'z' {
			return false
		}
	}
	return true
}

func allUppercaseAlphaNumeric(value string) bool {
	for _, character := range value {
		if (character < 'A' || character > 'Z') && (character < '0' || character > '9') {
			return false
		}
	}
	return true
}

func validAWSRoleSessionName(value string) bool {
	if len(value) < 2 || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if (character >= 'A' && character <= 'Z') || (character >= 'a' && character <= 'z') ||
			(character >= '0' && character <= '9') || strings.ContainsRune("+=,.@-", character) {
			continue
		}
		return false
	}
	return true
}
