package service

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha512"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/gaborage/go-bricks/jose"
	jositest "github.com/gaborage/go-bricks/jose/testing"
	josev4 "github.com/go-jose/go-jose/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testDetachedOurKid  = "tokens.detached.our"
	testDetachedPeerKid = "tokens.detached.peer"
	testPANPayload      = `{"pan":"` + testPAN + `"}`
	rs512Header         = `{"alg":"RS512"}`
)

// The jose refusal codes these tests pin.
const (
	joseMalformed     = "JOSE_MALFORMED"
	joseAlgDisallowed = "JOSE_ALGORITHM_DISALLOWED"
	joseKidUnknown    = "JOSE_KID_UNKNOWN"
	joseSigInvalid    = "JOSE_SIGNATURE_INVALID"
	joseCtyRejected   = "JOSE_CTY_REJECTED"
	joseDecryptFailed = "JOSE_DECRYPT_FAILED"
)

// detachedKeys are the gateway's two dedicated pairs (ours, the peer's), shared
// by every test here. Keys are read-only, so generating them per test would only
// cost time.
var detachedKeys = sync.OnceValue(func() [2]*rsa.PrivateKey {
	var keys [2]*rsa.PrivateKey
	for i := range keys {
		priv, err := rsa.GenerateKey(rand.Reader, 2048)
		if err != nil {
			panic(err)
		}
		keys[i] = priv
	}
	return keys
})

// detachedFixture is one keystore holding all four demo entries, as the module
// sees it: tokens.our and tokens.peer (with the MLE and VTS Issuer simulators
// over them) plus the two dedicated detached entries, and the gateway built as
// module.go builds it.
type detachedFixture struct {
	vts      *vtsFixture
	ourPriv  *rsa.PrivateKey // tokens.detached.our
	peerPriv *rsa.PrivateKey // tokens.detached.peer
	resolver jose.KeyResolver
	gateway  *DetachedGateway
}

func newDetachedFixture(t *testing.T) *detachedFixture {
	t.Helper()
	vts := newVTSFixture(t)
	keys := detachedKeys()
	our, peer := keys[0], keys[1]
	vts.mle.keystore.
		WithPrivateKey(testDetachedOurKid, our).WithPublicKey(testDetachedOurKid, &our.PublicKey).
		WithPrivateKey(testDetachedPeerKid, peer).WithPublicKey(testDetachedPeerKid, &peer.PublicKey)

	gateway, err := NewDetachedGateway(&DetachedGatewayConfig{
		KeyStore:   vts.mle.keystore,
		DecryptKid: testDetachedOurKid,
		VerifyKid:  testDetachedPeerKid,
		ReplyKid:   testDetachedPeerKid,
	})
	require.NoError(t, err)

	return &detachedFixture{
		vts:      vts,
		ourPriv:  our,
		peerPriv: peer,
		resolver: jositest.NewTestResolver(map[string]any{testDetachedOurKid: our, testDetachedPeerKid: peer}),
		gateway:  gateway,
	}
}

// privateKey reads one private half back out of the shared keystore, for a test
// that has to sign or open as an existing mode's key.
func (f *detachedFixture) privateKey(t *testing.T, kid string) *rsa.PrivateKey {
	t.Helper()
	priv, err := f.vts.mle.keystore.PrivateKey(kid)
	require.NoError(t, err)
	return priv
}

// detachedVector is one request as the peer sends it: the JWE for the body's
// data member, the detached signature over that exact string, and the attached
// form of the same JWS.
type detachedVector struct {
	jwe      string
	detached string
	attached string
}

// mint plays the peer with go-jose directly, as the framework's own fixture
// does: it encrypts payload to the gateway's public key and signs the JWE
// compact string with RS512, writing no kid on either token.
func (f *detachedFixture) mint(t *testing.T, payload string) detachedVector {
	t.Helper()
	jwe := mintJWE(t, &f.ourPriv.PublicKey, payload, josev4.A256GCM, "", nil)
	signer, err := josev4.NewSigner(josev4.SigningKey{Algorithm: josev4.RS512, Key: f.peerPriv}, nil)
	require.NoError(t, err)
	signed, err := signer.Sign([]byte(jwe))
	require.NoError(t, err)
	detached, err := signed.DetachedCompactSerialize()
	require.NoError(t, err)
	attached, err := signed.CompactSerialize()
	require.NoError(t, err)
	return detachedVector{jwe: jwe, detached: detached, attached: attached}
}

// mintJWE encrypts payload to pub with RSA-OAEP-256 and enc. A non-empty keyID
// writes a kid; opts may add protected headers.
func mintJWE(t *testing.T, pub *rsa.PublicKey, payload string, enc josev4.ContentEncryption, keyID string, opts *josev4.EncrypterOptions) string {
	t.Helper()
	recipient := josev4.Recipient{Algorithm: josev4.RSA_OAEP_256, Key: pub, KeyID: keyID}
	encrypter, err := josev4.NewEncrypter(enc, recipient, opts)
	require.NoError(t, err)
	encrypted, err := encrypter.Encrypt([]byte(payload))
	require.NoError(t, err)
	jwe, err := encrypted.CompactSerialize()
	require.NoError(t, err)
	return jwe
}

// signDetached hand-builds a detached JWS over jwe with exactly the protected
// header given, signed RS512 (RSASSA-PKCS1-v1_5 over SHA-512). It lets a test
// write headers go-jose would not, such as "kid": null, or an alg that does not
// match the signature: Open checks alg before it touches a key.
func signDetached(t *testing.T, priv *rsa.PrivateKey, header, jwe string) string {
	t.Helper()
	protected := base64.RawURLEncoding.EncodeToString([]byte(header))
	digest := sha512.Sum512([]byte(protected + "." + base64.RawURLEncoding.EncodeToString([]byte(jwe))))
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA512, digest[:])
	require.NoError(t, err)
	return protected + ".." + base64.RawURLEncoding.EncodeToString(sig)
}

// protectedHeader decodes the first segment of a compact serialization.
func protectedHeader(t *testing.T, compact string) map[string]any {
	t.Helper()
	raw, err := base64.RawURLEncoding.DecodeString(strings.SplitN(compact, ".", 2)[0])
	require.NoError(t, err)
	var members map[string]any
	require.NoError(t, json.Unmarshal(raw, &members))
	return members
}

// openReply plays the peer opening the reply data: a bare JWE sealed to its key
// that must carry no kid, cty or typ header.
func (f *detachedFixture) openReply(t *testing.T, sealed string) []byte {
	t.Helper()
	members := protectedHeader(t, sealed)
	assert.NotContains(t, members, "kid")
	assert.NotContains(t, members, "cty")
	assert.NotContains(t, members, "typ")
	assert.Equal(t, "RSA-OAEP-256", members["alg"])
	assert.Equal(t, "A256GCM", members["enc"])

	plaintext, _ := jositest.OpenForTest(t, sealed, newDetachedReplyOpenPolicy(testDetachedPeerKid), f.resolver)
	return plaintext
}

// requireJOSECode asserts err is a *jose.Error with the given code and status.
func requireJOSECode(t *testing.T, err error, code string, status int) {
	t.Helper()
	var jerr *jose.Error
	require.True(t, errors.As(err, &jerr), "want a *jose.Error, got %v", err)
	assert.Equal(t, code, jerr.Code)
	assert.Equal(t, status, jerr.Status)
}

func TestDetachedGatewayRoundTrip(t *testing.T) {
	f := newDetachedFixture(t)
	v := f.mint(t, testPANPayload)

	sealed, err := f.gateway.Process(context.Background(), v.detached, v.jwe)
	require.NoError(t, err)

	var reply struct {
		Token struct {
			Last4     string `json:"last4"`
			MaskedPAN string `json:"masked_pan"`
		} `json:"token"`
	}
	require.NoError(t, json.Unmarshal(f.openReply(t, sealed), &reply))
	assert.Equal(t, "1111", reply.Token.Last4)
	assert.NotContains(t, reply.Token.MaskedPAN, testPAN)
}

// A kid that is present is still judged: equal to the entry name it passes, and
// an outer cty of exactly JWE passes. Only an ABSENT kid is what the flag excuses.
func TestDetachedGatewayAcceptsMatchingKidAndJWECty(t *testing.T) {
	f := newDetachedFixture(t)
	jwe := mintJWE(t, &f.ourPriv.PublicKey, testPANPayload, josev4.A256GCM, testDetachedOurKid, nil)

	for name, header := range map[string]string{
		"outer kid equal to the entry name": `{"alg":"RS512","kid":"` + testDetachedPeerKid + `"}`,
		"outer cty JWE":                     `{"alg":"RS512","cty":"JWE"}`,
	} {
		t.Run(name, func(t *testing.T) {
			sealed, err := f.gateway.Process(context.Background(), signDetached(t, f.peerPriv, header, jwe), jwe)
			require.NoError(t, err)
			assert.NotEmpty(t, f.openReply(t, sealed))
		})
	}
}

// TestDetachedGatewayRefusals pins every jose refusal the gateway can meet, by
// code and status. The handler maps these codes to its own vocabulary.
func TestDetachedGatewayRefusals(t *testing.T) {
	f := newDetachedFixture(t)
	v := f.mint(t, testPANPayload)
	other := f.mint(t, testPANPayload)
	ourPub := &f.ourPriv.PublicKey

	innerCtyJWS := mintJWE(t, ourPub, testPANPayload, josev4.A256GCM, "",
		(&josev4.EncrypterOptions{}).WithContentType("JWS"))
	innerA128 := mintJWE(t, ourPub, testPANPayload, josev4.A128GCM, "", nil)
	innerWrongKid := mintJWE(t, ourPub, testPANPayload, josev4.A256GCM, testOurKid, nil)
	innerEmptyKid := mintJWE(t, ourPub, testPANPayload, josev4.A256GCM, "", (&josev4.EncrypterOptions{}).WithHeader("kid", ""))
	innerNullKid := mintJWE(t, ourPub, testPANPayload, josev4.A256GCM, "", (&josev4.EncrypterOptions{}).WithHeader("kid", nil))
	require.Contains(t, protectedHeader(t, innerEmptyKid), "kid", `the inner header must carry "kid": ""`)
	require.Contains(t, protectedHeader(t, innerNullKid), "kid", `the inner header must carry "kid": null`)
	require.Nil(t, protectedHeader(t, innerNullKid)["kid"])
	signedByOtherKey := signDetached(t, f.privateKey(t, testPeerKid), rs512Header, v.jwe)

	tests := []struct {
		name       string
		detached   string
		jwe        string
		wantCode   string
		wantStatus int
	}{
		// AttachDetachedJWS: framing, no crypto.
		{"attached compact in the header", v.attached, v.jwe, joseMalformed, 400},
		{"five-segment JWE in the header", v.jwe, v.jwe, joseMalformed, 400},
		{"JSON serialization in the header", `{"protected":"e30","signature":"AA"}`, v.jwe, joseMalformed, 400},
		{"scheme left on the value", "JWS " + v.detached, v.jwe, joseMalformed, 400},
		{"empty data", v.detached, "", joseMalformed, 400},
		// Open, outer JWS: alg is checked before any key, then kid, then signature.
		{"outer alg RS256", signDetached(t, f.peerPriv, `{"alg":"RS256"}`, v.jwe), v.jwe, joseAlgDisallowed, 400},
		{"outer alg PS256", signDetached(t, f.peerPriv, `{"alg":"PS256"}`, v.jwe), v.jwe, joseAlgDisallowed, 400},
		{"outer kid of another entry", signDetached(t, f.peerPriv, `{"alg":"RS512","kid":"tokens.peer"}`, v.jwe), v.jwe, joseKidUnknown, 401},
		{"outer kid empty string", signDetached(t, f.peerPriv, `{"alg":"RS512","kid":""}`, v.jwe), v.jwe, joseKidUnknown, 401},
		{"outer kid null", signDetached(t, f.peerPriv, `{"alg":"RS512","kid":null}`, v.jwe), v.jwe, joseKidUnknown, 401},
		{"outer kid not a string", signDetached(t, f.peerPriv, `{"alg":"RS512","kid":7}`, v.jwe), v.jwe, "JOSE_OUTER_NOT_JWS", 400},
		{"data altered after signing", v.detached, v.jwe + "A", joseSigInvalid, 401},
		{"data swapped for another valid JWE", v.detached, other.jwe, joseSigInvalid, 401},
		{"signed by another key", signedByOtherKey, v.jwe, joseSigInvalid, 401},
		{"outer cty JWS", signDetached(t, f.peerPriv, `{"alg":"RS512","cty":"JWS"}`, v.jwe), v.jwe, joseCtyRejected, 400},
		{"outer cty empty string", signDetached(t, f.peerPriv, `{"alg":"RS512","cty":""}`, v.jwe), v.jwe, joseCtyRejected, 400},
		// Open, inner JWE: reached only after the outer signature verified.
		{"inner cty JWS", signDetached(t, f.peerPriv, rs512Header, innerCtyJWS), innerCtyJWS, joseCtyRejected, 400},
		{"inner enc A128GCM", signDetached(t, f.peerPriv, rs512Header, innerA128), innerA128, joseMalformed, 400},
		{"inner kid of another entry", signDetached(t, f.peerPriv, rs512Header, innerWrongKid), innerWrongKid, joseKidUnknown, 401},
		{"inner kid empty string", signDetached(t, f.peerPriv, rs512Header, innerEmptyKid), innerEmptyKid, joseKidUnknown, 401},
		{"inner kid null", signDetached(t, f.peerPriv, rs512Header, innerNullKid), innerNullKid, joseKidUnknown, 401},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sealed, err := f.gateway.Process(context.Background(), tt.detached, tt.jwe)
			requireJOSECode(t, err, tt.wantCode, tt.wantStatus)
			assert.Empty(t, sealed)
		})
	}
}

// A request that verified and decrypted but whose plaintext is not a valid
// {"pan"} request is a caller error, distinct from a jose refusal.
func TestDetachedGatewayRefusesInvalidPlaintext(t *testing.T) {
	f := newDetachedFixture(t)

	notJSON := f.mint(t, "not json")
	_, err := f.gateway.Process(context.Background(), notJSON.detached, notJSON.jwe)
	require.ErrorIs(t, err, ErrInvalidPayload)
	assert.NotErrorIs(t, err, ErrInvalidPAN)
	var jerr *jose.Error
	assert.False(t, errors.As(err, &jerr), "a payload refusal is not a jose refusal")

	badLuhn := f.mint(t, `{"pan":"4111111111111112"}`)
	_, err = f.gateway.Process(context.Background(), badLuhn.detached, badLuhn.jwe)
	require.ErrorIs(t, err, ErrInvalidPAN)
}

// AcceptMissingKid is what lets the kid-less peer in: the same vector under the
// same policy with the flag off is JOSE_KID_MISSING.
func TestDetachedPolicyNeedsAcceptMissingKid(t *testing.T) {
	f := newDetachedFixture(t)
	v := f.mint(t, testPANPayload)

	strict := NewDetachedInboundPolicy(testDetachedOurKid, testDetachedPeerKid)
	strict.AcceptMissingKid = false
	plaintext, _, _, err := jose.Open(v.attached, strict, f.resolver)
	requireJOSECode(t, err, "JOSE_KID_MISSING", 401)
	assert.Empty(t, plaintext)
}

// The policy grammar the gateway relies on: the minimal mode is open-only and
// RS512-only, and OmitKid belongs to bare mode.
func TestDetachedPolicyGrammar(t *testing.T) {
	require.NoError(t, NewDetachedInboundPolicy(testDetachedOurKid, testDetachedPeerKid).Validate())
	require.NoError(t, NewDetachedReplyPolicy(testDetachedPeerKid).Validate())

	omitKid := NewDetachedInboundPolicy(testDetachedOurKid, testDetachedPeerKid)
	omitKid.OmitKid = true

	outbound := NewDetachedInboundPolicy("", "")
	outbound.Direction = jose.DirectionOutbound
	outbound.AcceptMissingKid = false
	outbound.SignKid = testDetachedPeerKid
	outbound.EncryptKid = testDetachedOurKid

	ps256 := NewDetachedInboundPolicy(testDetachedOurKid, testDetachedPeerKid)
	ps256.SigAlg = josev4.PS256

	unset := NewDetachedInboundPolicy(testDetachedOurKid, testDetachedPeerKid)
	unset.SigAlg = ""

	for name, tc := range map[string]struct {
		policy *jose.Policy
		code   string
	}{
		"OmitKid on the minimal mode": {omitKid, "JOSE_POLICY_MODE_MISMATCH"},
		"an outbound minimal policy":  {outbound, "JOSE_POLICY_DIRECTION_MISMATCH"},
		"PS256 instead of RS512":      {ps256, joseAlgDisallowed},
		"SigAlg left unset":           {unset, joseAlgDisallowed},
	} {
		t.Run(name, func(t *testing.T) {
			requireJOSECode(t, tc.policy.Validate(), tc.code, 0)
		})
	}
}

// A keystore that cannot resolve the verify key surfaces a jose error with no
// status: the gateway's own fault, which the handler answers 500.
func TestDetachedGatewayKeyLookupFailureCarriesNoStatus(t *testing.T) {
	f := newDetachedFixture(t)
	v := f.mint(t, testPANPayload)

	gateway, err := NewDetachedGateway(&DetachedGatewayConfig{
		KeyStore:   f.vts.mle.keystore,
		DecryptKid: testDetachedOurKid,
		VerifyKid:  "tokens.detached.absent",
		ReplyKid:   testDetachedPeerKid,
	})
	require.NoError(t, err, "construction validates policies; it resolves no key")

	_, err = gateway.Process(context.Background(), v.detached, v.jwe)
	requireJOSECode(t, err, joseKidUnknown, 0)
}

func TestNewDetachedGatewayFailsFast(t *testing.T) {
	_, err := NewDetachedGateway(nil)
	require.Error(t, err)

	_, err = NewDetachedGateway(&DetachedGatewayConfig{DecryptKid: testDetachedOurKid, VerifyKid: testDetachedPeerKid, ReplyKid: testDetachedPeerKid})
	require.ErrorContains(t, err, "keystore")

	f := newDetachedFixture(t)
	_, err = NewDetachedGateway(&DetachedGatewayConfig{KeyStore: f.vts.mle.keystore, DecryptKid: testDetachedOurKid, ReplyKid: testDetachedPeerKid})
	require.ErrorIs(t, err, jose.ErrPolicyMismatch, "the minimal inbound policy needs a verify kid")
	assert.ErrorContains(t, err, "detached peer inbound policy")
}
