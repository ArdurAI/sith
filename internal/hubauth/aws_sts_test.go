// SPDX-License-Identifier: Apache-2.0

package hubauth

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ArdurAI/sith/internal/tenancy"
)

const awsSTSTestEndpoint = "https://sts.us-east-1.amazonaws.com"

func TestAWSSTSVerifierExchangesPinnedAssumedRole(t *testing.T) {
	now := time.Date(2026, 7, 12, 16, 0, 0, 0, time.UTC)
	rawProof, signedURL := awsSTSTestProof(t, awsSTSTestEndpoint, now.Add(-time.Second), 60)
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if request.Method != http.MethodGet || request.Host != "sts.us-east-1.amazonaws.com" ||
			request.URL.Path != "/" || request.URL.RawQuery != signedURL.RawQuery ||
			request.Header.Get(awsSTSAudienceHeader) != "https://hub.sith.test" {
			http.Error(response, "rejected", http.StatusForbidden)
			return
		}
		writeAWSSTSIdentityResponse(response, "123456789012", "arn:aws:sts::123456789012:assumed-role/sith-reader/build-42", "AROAEXAMPLE123456:build-42")
	}))
	defer server.Close()
	verifier := newAWSTestVerifier(t, now, server)

	principal, err := verifier.Verify(context.Background(), rawProof)
	if err != nil {
		t.Fatal(err)
	}
	if principal.Identity != (CloudIdentity{Provider: CloudProviderAWS, Realm: "123456789012", Subject: "role-id:AROAEXAMPLE123456"}) ||
		principal.Audience != "https://hub.sith.test" || !principal.IssuedAt.Equal(now.Add(-time.Second)) || !principal.ExpiresAt.Equal(now.Add(59*time.Second)) {
		t.Fatalf("verified principal = %#v", principal)
	}

	service, _, sessionVerifier, admin := newCloudTestFixture(t, &now, nil)
	service.verifiers[CloudProviderAWS] = verifier
	if err := service.BindIdentity(context.Background(), admin, principal.Identity, "user:alice"); err != nil {
		t.Fatal(err)
	}
	session, err := service.Exchange(context.Background(), "workspace-a", CloudProviderAWS, rawProof)
	if err != nil {
		t.Fatal(err)
	}
	issuedPrincipal, err := sessionVerifier.Verify(context.Background(), session.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	scope, err := issuedPrincipal.Scope("workspace-a")
	if err != nil || scope.Subject() != "user:alice" || scope.Role() != tenancy.RoleReader {
		t.Fatalf("issued scope = %#v, error = %v", scope, err)
	}
	if _, err := service.Exchange(context.Background(), "workspace-a", CloudProviderAWS, rawProof); err == nil {
		t.Fatal("replayed STS proof minted a second session")
	}
	if calls.Load() != 3 {
		t.Fatalf("STS calls = %d, want 3 (direct, exchange, rejected replay)", calls.Load())
	}
}

func TestAWSSTSVerifierRejectsMalformedAndAlteredProofs(t *testing.T) {
	now := time.Date(2026, 7, 12, 16, 0, 0, 0, time.UTC)
	rawProof, signedURL := awsSTSTestProof(t, awsSTSTestEndpoint, now.Add(-time.Second), 60)
	var calls atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		if request.URL.RawQuery != signedURL.RawQuery || request.Header.Get(awsSTSAudienceHeader) != "https://hub.sith.test" {
			http.Error(response, "signature mismatch", http.StatusForbidden)
			return
		}
		writeAWSSTSIdentityResponse(response, "123456789012", "arn:aws:sts::123456789012:assumed-role/sith-reader/build-42", "AROAEXAMPLE123456:build-42")
	}))
	defer server.Close()
	verifier := newAWSTestVerifier(t, now, server)

	tests := map[string]struct {
		rawProof      string
		forwardsToSTS bool
	}{
		"non base64 proof": {rawProof: "not-a-presigned-url"},
		"expired": {rawProof: mutateAWSSTSTestProof(t, rawProof, func(query url.Values) {
			query.Set("X-Amz-Date", now.Add(-2*time.Minute).Format("20060102T150405Z"))
		})},
		"future": {rawProof: mutateAWSSTSTestProof(t, rawProof, func(query url.Values) {
			query.Set("X-Amz-Date", now.Add(time.Second).Format("20060102T150405Z"))
		})},
		"unbounded expiry": {rawProof: mutateAWSSTSTestProof(t, rawProof, func(query url.Values) {
			query.Set("X-Amz-Expires", "61")
		})},
		"wrong action": {rawProof: mutateAWSSTSTestProof(t, rawProof, func(query url.Values) {
			query.Set("Action", "AssumeRole")
		})},
		"wrong signed headers": {rawProof: mutateAWSSTSTestProof(t, rawProof, func(query url.Values) {
			query.Set("X-Amz-SignedHeaders", "host;x-untrusted-audience")
		})},
		"wrong service": {rawProof: mutateAWSSTSTestProof(t, rawProof, func(query url.Values) {
			query.Set("X-Amz-Credential", "ASIAEXAMPLE123456/20260712/us-east-1/iam/aws4_request")
		})},
		"duplicate query value": {rawProof: base64.RawURLEncoding.EncodeToString([]byte(signedURL.String() + "&Action=GetCallerIdentity"))},
		"endpoint fallback":     {rawProof: awsSTSProofForURL(t, mustAWSSTSURL(t, "https://sts.amazonaws.com/?"+signedURL.RawQuery))},
		"altered signed request": {rawProof: mutateAWSSTSTestProof(t, rawProof, func(query url.Values) {
			query.Set("X-Amz-Signature", strings.Repeat("b", 64))
		}), forwardsToSTS: true},
	}
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			before := calls.Load()
			if _, err := verifier.Verify(context.Background(), test.rawProof); err == nil {
				t.Fatal("unsafe AWS STS proof was accepted")
			}
			gotForwards := calls.Load() - before
			wantForwards := int32(0)
			if test.forwardsToSTS {
				wantForwards = 1
			}
			if gotForwards != wantForwards {
				t.Fatalf("STS forwards = %d, want %d", gotForwards, wantForwards)
			}
		})
	}
}

func TestAWSSTSVerifierRejectsLongLivedAndCrossPartitionResponses(t *testing.T) {
	now := time.Date(2026, 7, 12, 16, 0, 0, 0, time.UTC)
	rawProof, _ := awsSTSTestProof(t, awsSTSTestEndpoint, now.Add(-time.Second), 60)
	for name, response := range map[string]struct {
		account string
		arn     string
		userID  string
	}{
		"long lived IAM user": {
			account: "123456789012", arn: "arn:aws:iam::123456789012:user/alice", userID: "AIDAEXAMPLE123456",
		},
		"cross partition ARN": {
			account: "123456789012", arn: "arn:aws-cn:sts::123456789012:assumed-role/sith-reader/build-42", userID: "AROAEXAMPLE123456:build-42",
		},
		"wrong account": {
			account: "123456789012", arn: "arn:aws:sts::999999999999:assumed-role/sith-reader/build-42", userID: "AROAEXAMPLE123456:build-42",
		},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writeAWSSTSIdentityResponse(writer, response.account, response.arn, response.userID)
			}))
			defer server.Close()
			verifier := newAWSTestVerifier(t, now, server)
			if _, err := verifier.Verify(context.Background(), rawProof); err == nil {
				t.Fatal("unsafe STS response was accepted")
			}
		})
	}
}

func TestAWSSTSVerifierRejectsProofForAnotherAudience(t *testing.T) {
	now := time.Date(2026, 7, 12, 16, 0, 0, 0, time.UTC)
	rawProof, _ := awsSTSTestProof(t, awsSTSTestEndpoint, now.Add(-time.Second), 60)
	server := httptest.NewTLSServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get(awsSTSAudienceHeader) != "https://hub.sith.test" {
			http.Error(response, "audience changes the signed canonical request", http.StatusForbidden)
			return
		}
		writeAWSSTSIdentityResponse(response, "123456789012", "arn:aws:sts::123456789012:assumed-role/sith-reader/build-42", "AROAEXAMPLE123456:build-42")
	}))
	defer server.Close()
	verifier := newAWSTestVerifierForAudience(t, now, server, "https://different-hub.sith.test")
	if _, err := verifier.Verify(context.Background(), rawProof); err == nil {
		t.Fatal("proof signed for a different audience was accepted")
	}
}

func TestAWSSTSVerifierConfigRejectsUnpinnedEndpoints(t *testing.T) {
	for _, endpoint := range []string{
		"http://sts.us-east-1.amazonaws.com", "https://sts.amazonaws.com", "https://sts.us-east-1.amazonaws.com/escape",
		"https://sts.us-east-1.evil.example", "https://sts.cn-north-1.amazonaws.com", "https://sts.us-east-1.amazonaws.com?endpoint=evil",
	} {
		if _, err := NewAWSSTSVerifier(AWSSTSVerifierConfig{Endpoint: endpoint, Audience: "https://hub.sith.test"}); err == nil {
			t.Errorf("unsafe endpoint %q was accepted", endpoint)
		}
	}
	for _, endpoint := range []string{
		"https://sts.us-east-1.amazonaws.com", "https://sts.us-gov-west-1.amazonaws.com", "https://sts.cn-north-1.amazonaws.com.cn",
	} {
		if _, err := NewAWSSTSVerifier(AWSSTSVerifierConfig{Endpoint: endpoint, Audience: "https://hub.sith.test"}); err != nil {
			t.Errorf("valid endpoint %q rejected: %v", endpoint, err)
		}
	}
}

func newAWSTestVerifier(t *testing.T, now time.Time, server *httptest.Server) *AWSSTSVerifier {
	return newAWSTestVerifierForAudience(t, now, server, "https://hub.sith.test")
}

func newAWSTestVerifierForAudience(t *testing.T, now time.Time, server *httptest.Server, audience string) *AWSSTSVerifier {
	t.Helper()
	serverURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	transport := server.Client().Transport.(*http.Transport).Clone()
	dialer := net.Dialer{}
	transport.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, "tcp", serverURL.Host)
	}
	transport.TLSClientConfig = transport.TLSClientConfig.Clone()
	// #nosec G402 -- this isolated TLS emulator is reached through a test-only dial override.
	transport.TLSClientConfig.InsecureSkipVerify = true
	client := &http.Client{Transport: transport}
	verifier, err := newAWSSTSVerifier(AWSSTSVerifierConfig{
		Endpoint: awsSTSTestEndpoint, Audience: audience, Now: func() time.Time { return now },
	}, client)
	if err != nil {
		t.Fatal(err)
	}
	return verifier
}

func awsSTSTestProof(t *testing.T, endpoint string, issuedAt time.Time, expiresIn int) (string, *url.URL) {
	t.Helper()
	parsed := mustAWSSTSURL(t, endpoint)
	parsed.Path = "/"
	query := url.Values{
		"Action":              {"GetCallerIdentity"},
		"Version":             {"2011-06-15"},
		"X-Amz-Algorithm":     {"AWS4-HMAC-SHA256"},
		"X-Amz-Credential":    {"ASIAEXAMPLE123456/" + issuedAt.Format("20060102") + "/us-east-1/sts/aws4_request"},
		"X-Amz-Date":          {issuedAt.Format("20060102T150405Z")},
		"X-Amz-Expires":       {fmt.Sprintf("%d", expiresIn)},
		"X-Amz-SignedHeaders": {"host;x-sith-audience"},
		"X-Amz-Signature":     {strings.Repeat("a", 64)},
	}
	parsed.RawQuery = query.Encode()
	return awsSTSProofForURL(t, parsed), parsed
}

func mutateAWSSTSTestProof(t *testing.T, rawProof string, mutate func(url.Values)) string {
	t.Helper()
	decoded, err := base64.RawURLEncoding.DecodeString(rawProof)
	if err != nil {
		t.Fatal(err)
	}
	parsed := mustAWSSTSURL(t, string(decoded))
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		t.Fatal(err)
	}
	mutate(query)
	parsed.RawQuery = query.Encode()
	return awsSTSProofForURL(t, parsed)
}

func mustAWSSTSURL(t *testing.T, rawURL string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}

func awsSTSProofForURL(t *testing.T, signedURL *url.URL) string {
	t.Helper()
	return base64.RawURLEncoding.EncodeToString([]byte(signedURL.String()))
}

func writeAWSSTSIdentityResponse(writer http.ResponseWriter, account, arn, userID string) {
	writer.Header().Set("Content-Type", "text/xml")
	_, _ = fmt.Fprintf(writer, `<GetCallerIdentityResponse xmlns="%s"><GetCallerIdentityResult><Arn>%s</Arn><UserId>%s</UserId><Account>%s</Account></GetCallerIdentityResult></GetCallerIdentityResponse>`, awsSTSXMLNamespace, arn, userID, account)
}
