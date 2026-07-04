// Opt-in Cloudflare Access JWT verification — see THREAT-MODEL.md for setup.

var TEAM_DOMAIN = 'https://YOUR-TEAM.cloudflareaccess.com';
var AUD = 'YOUR-ACCESS-APPLICATION-AUD-TAG';

var jwksCache = { keys: null, at: 0 };
var JWKS_TTL_MS = 3600 * 1000;

async function fetchJwks() {
    var now = Date.now();
    if (jwksCache.keys && now - jwksCache.at < JWKS_TTL_MS) {
        return jwksCache.keys;
    }
    var res = await ngx.fetch(TEAM_DOMAIN + '/cdn-cgi/access/certs');
    var body = await res.json();
    jwksCache = { keys: body.keys, at: now };
    return body.keys;
}

function b64urlToBuf(s) {
    s = s.replace(/-/g, '+').replace(/_/g, '/');
    while (s.length % 4) s += '=';
    return Buffer.from(s, 'base64');
}

async function verify(r) {
    try {
        var token = (r.headersIn['Cf-Access-Jwt-Assertion'] || '').trim();
        if (!token) {
            var m = (r.headersIn['Cookie'] || '').match(/CF_Authorization=([^;]+)/);
            token = m ? m[1] : '';
        }
        if (!token) return r.return(401, 'no access token\n');

        var parts = token.split('.');
        if (parts.length !== 3) return r.return(401, 'malformed token\n');

        var header = JSON.parse(b64urlToBuf(parts[0]).toString());
        var payload = JSON.parse(b64urlToBuf(parts[1]).toString());

        var auds = Array.isArray(payload.aud) ? payload.aud : [payload.aud];
        if (auds.indexOf(AUD) < 0) return r.return(403, 'bad aud\n');
        if (payload.iss !== TEAM_DOMAIN) return r.return(403, 'bad iss\n');
        if (!payload.exp || Date.now() / 1000 > payload.exp) return r.return(403, 'expired\n');

        var keys = await fetchJwks();
        var jwk = null;
        for (var i = 0; i < keys.length; i++) {
            if (keys[i].kid === header.kid) { jwk = keys[i]; break; }
        }
        if (!jwk) return r.return(403, 'unknown kid\n');

        var key = await crypto.subtle.importKey(
            'jwk', jwk,
            { name: 'RSASSA-PKCS1-v1_5', hash: 'SHA-256' },
            false, ['verify']
        );
        var signed = Buffer.from(parts[0] + '.' + parts[1]);
        var ok = await crypto.subtle.verify(
            'RSASSA-PKCS1-v1_5', key, b64urlToBuf(parts[2]), signed
        );
        if (!ok) return r.return(403, 'bad signature\n');

        r.variables.vswarm_verified_email = payload.email || '';
        return r.return(204);
    } catch (e) {
        return r.return(500, 'verify error\n');
    }
}

export default { verify };
