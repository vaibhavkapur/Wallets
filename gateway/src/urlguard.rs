//! Destination validation: URL shape, endpoint-prefix allowlisting, and
//! SSRF protection with DNS pinning.
//!
//! Agent-supplied URLs are untrusted. Before any connection is made the
//! gateway (1) normalizes and structurally validates the URL, (2) checks it
//! against the owner-approved endpoint prefix returned by the control plane,
//! (3) resolves the host and rejects any address in a private, loopback,
//! link-local, multicast, unspecified, or carrier-grade NAT range, and (4)
//! pins the validated addresses for the actual connection so a DNS answer
//! cannot change between validation and use.

use std::net::{IpAddr, Ipv4Addr, Ipv6Addr, SocketAddr};
use url::{Host, Url};

#[derive(Debug, thiserror::Error, PartialEq, Eq)]
pub enum UrlError {
    #[error("url is invalid: {0}")]
    Invalid(String),
    #[error("scheme {0:?} is not permitted")]
    Scheme(String),
    #[error("userinfo is not permitted in resource urls")]
    UserInfo,
    #[error("fragments are not permitted in resource urls")]
    Fragment,
    #[error("url is outside the approved endpoint {0}")]
    OutsideAllowlist(String),
    #[error("destination resolves to a forbidden address: {0}")]
    ForbiddenAddress(IpAddr),
    #[error("destination could not be resolved: {0}")]
    Unresolvable(String),
    #[error("port {0} is not permitted")]
    Port(u16),
}

#[derive(Debug, Clone)]
pub struct Policy {
    /// Allow plain http (development only).
    pub allow_http: bool,
    /// Allow loopback/private targets (development only, for local mock servers).
    pub allow_private_targets: bool,
}

/// Parse and normalize an absolute resource URL.
pub fn normalize(raw: &str, policy: &Policy) -> Result<Url, UrlError> {
    let mut u = Url::parse(raw).map_err(|e| UrlError::Invalid(e.to_string()))?;
    match u.scheme() {
        "https" => {}
        "http" if policy.allow_http => {}
        s => return Err(UrlError::Scheme(s.to_string())),
    }
    if !u.username().is_empty() || u.password().is_some() {
        return Err(UrlError::UserInfo);
    }
    if u.fragment().is_some() {
        return Err(UrlError::Fragment);
    }
    if u.host().is_none() || u.host_str().map(|h| h.is_empty()).unwrap_or(true) {
        return Err(UrlError::Invalid("missing host".into()));
    }
    if let Some(port) = u.port() {
        // Only well-known web ports and the unprivileged range used by local
        // development; never arbitrary low ports (SMTP, Redis, etc.).
        if !(port == 80 || port == 443 || port >= 1024) {
            return Err(UrlError::Port(port));
        }
    }
    if u.path().is_empty() {
        u.set_path("/");
    }
    // url crate lowercases the host already; ensure the scheme is too.
    Ok(u)
}

/// Check that `u` is under the approved prefix (scheme, host, port, and a
/// path prefix ending on a segment boundary). Mirrors the Go implementation.
pub fn within_prefix(prefix: &str, u: &Url) -> Result<(), UrlError> {
    let p = Url::parse(prefix).map_err(|_| UrlError::OutsideAllowlist(prefix.to_string()))?;
    let same_origin = p.scheme() == u.scheme()
        && p.host_str().map(|h| h.to_ascii_lowercase()) == u.host_str().map(|h| h.to_ascii_lowercase())
        && p.port_or_known_default() == u.port_or_known_default();
    if !same_origin {
        return Err(UrlError::OutsideAllowlist(prefix.to_string()));
    }
    let pp = if p.path().is_empty() { "/" } else { p.path() };
    if pp == "/" {
        return Ok(());
    }
    let pp = pp.trim_end_matches('/');
    let path = u.path();
    if path == pp || path.starts_with(&format!("{pp}/")) {
        Ok(())
    } else {
        Err(UrlError::OutsideAllowlist(prefix.to_string()))
    }
}

/// True when the address must never be contacted on behalf of an agent.
pub fn is_forbidden_ip(ip: IpAddr) -> bool {
    match ip {
        IpAddr::V4(v4) => is_forbidden_v4(v4),
        IpAddr::V6(v6) => {
            if let Some(v4) = v6.to_ipv4_mapped() {
                return is_forbidden_v4(v4);
            }
            v6.is_loopback()
                || v6.is_unspecified()
                || v6.is_multicast()
                || is_unique_local(v6)
                || is_link_local_v6(v6)
                || is_v4_compatible_or_nat64(v6)
        }
    }
}

fn is_forbidden_v4(v4: Ipv4Addr) -> bool {
    let o = v4.octets();
    v4.is_private()
        || v4.is_loopback()
        || v4.is_link_local()
        || v4.is_broadcast()
        || v4.is_multicast()
        || v4.is_unspecified()
        || v4.is_documentation()
        || o[0] == 0
        || (o[0] == 100 && (64..=127).contains(&o[1])) // 100.64.0.0/10 CGNAT
        || (o[0] == 192 && o[1] == 0 && o[2] == 0) // 192.0.0.0/24 IETF protocol assignments
        || (o[0] == 198 && (18..=19).contains(&o[1])) // 198.18.0.0/15 benchmarking
        || o[0] >= 240 // reserved + broadcast
}

fn is_unique_local(v6: Ipv6Addr) -> bool {
    (v6.segments()[0] & 0xfe00) == 0xfc00
}

fn is_link_local_v6(v6: Ipv6Addr) -> bool {
    (v6.segments()[0] & 0xffc0) == 0xfe80
}

fn is_v4_compatible_or_nat64(v6: Ipv6Addr) -> bool {
    let s = v6.segments();
    // 64:ff9b::/96 NAT64 and ::a.b.c.d deprecated v4-compatible addresses.
    (s[0] == 0x64 && s[1] == 0xff9b) || (s[..6].iter().all(|x| *x == 0) && (s[6] != 0 || s[7] != 0))
}

/// Resolve the URL's host and return pinned socket addresses that passed the
/// SSRF checks. Every address returned by DNS must be acceptable; a mixed
/// answer (one public, one private) is rejected outright.
pub async fn resolve_pinned(u: &Url, policy: &Policy) -> Result<Vec<SocketAddr>, UrlError> {
    let port = u.port_or_known_default().unwrap_or(443);
    let addrs: Vec<IpAddr> = match u.host() {
        Some(Host::Ipv4(ip)) => vec![IpAddr::V4(ip)],
        Some(Host::Ipv6(ip)) => vec![IpAddr::V6(ip)],
        Some(Host::Domain(d)) => {
            let resolved = tokio::net::lookup_host((d, port)).await.map_err(|e| UrlError::Unresolvable(e.to_string()))?;
            resolved.map(|sa| sa.ip()).collect()
        }
        None => return Err(UrlError::Invalid("missing host".into())),
    };
    if addrs.is_empty() {
        return Err(UrlError::Unresolvable(u.host_str().unwrap_or("").to_string()));
    }
    if !policy.allow_private_targets {
        if let Some(bad) = addrs.iter().copied().find(|ip| is_forbidden_ip(*ip)) {
            return Err(UrlError::ForbiddenAddress(bad));
        }
    }
    Ok(addrs.into_iter().map(|ip| SocketAddr::new(ip, port)).collect())
}

#[cfg(test)]
mod tests {
    use super::*;
    use proptest::prelude::*;

    fn strict() -> Policy {
        Policy { allow_http: false, allow_private_targets: false }
    }

    #[test]
    fn rejects_dangerous_shapes() {
        let p = strict();
        assert_eq!(normalize("http://api.example.test/x", &p), Err(UrlError::Scheme("http".into())));
        assert_eq!(normalize("ftp://api.example.test/x", &p), Err(UrlError::Scheme("ftp".into())));
        assert_eq!(normalize("https://user:pw@api.example.test/x", &p), Err(UrlError::UserInfo));
        assert_eq!(normalize("https://api.example.test/x#frag", &p), Err(UrlError::Fragment));
        assert_eq!(normalize("https://api.example.test:25/x", &p), Err(UrlError::Port(25)));
        assert!(matches!(normalize("not a url", &p), Err(UrlError::Invalid(_))));
        assert!(matches!(normalize("https://", &p), Err(UrlError::Invalid(_))));
        assert!(matches!(normalize("/relative/path", &p), Err(UrlError::Invalid(_))));
        let ok = normalize("HTTPS://API.Example.test", &p).unwrap();
        assert_eq!(ok.as_str(), "https://api.example.test/");
    }

    #[test]
    fn prefix_matching_is_segment_aware() {
        let p = strict();
        let u = |s: &str| normalize(s, &p).unwrap();
        assert!(within_prefix("https://api.example.test/v1", &u("https://api.example.test/v1/weather?x=1")).is_ok());
        assert!(within_prefix("https://api.example.test/v1", &u("https://api.example.test/v1")).is_ok());
        assert!(within_prefix("https://api.example.test/v1/", &u("https://api.example.test/v1/x")).is_ok());
        assert!(within_prefix("https://api.example.test", &u("https://api.example.test/anything")).is_ok());
        assert!(within_prefix("https://api.example.test/v1", &u("https://api.example.test/v1x")).is_err());
        assert!(within_prefix("https://api.example.test/v1", &u("https://api.example.test/v2/x")).is_err());
        assert!(within_prefix("https://api.example.test/v1", &u("https://evil.test/v1/x")).is_err());
        assert!(within_prefix("https://api.example.test/v1", &u("https://api.example.test:8443/v1/x")).is_err());
        assert!(within_prefix("https://api.example.test/v1", &u("https://api.example.test.evil.test/v1/x")).is_err());
    }

    #[test]
    fn forbidden_addresses() {
        for s in [
            "127.0.0.1", "10.1.2.3", "172.16.0.1", "172.31.255.255", "192.168.1.1", "169.254.169.254", "0.0.0.0",
            "100.64.0.1", "100.127.255.255", "198.18.0.1", "224.0.0.1", "255.255.255.255", "::1", "::", "fc00::1",
            "fd12::1", "fe80::1", "::ffff:127.0.0.1", "::ffff:10.0.0.1", "64:ff9b::7f00:1", "ff02::1",
        ] {
            let ip: IpAddr = s.parse().unwrap();
            assert!(is_forbidden_ip(ip), "{s} must be forbidden");
        }
        for s in ["8.8.8.8", "1.1.1.1", "172.32.0.1", "100.128.0.1", "2606:4700:4700::1111"] {
            let ip: IpAddr = s.parse().unwrap();
            assert!(!is_forbidden_ip(ip), "{s} must be allowed");
        }
    }

    #[tokio::test]
    async fn literal_private_hosts_are_rejected_before_connecting() {
        let p = Policy { allow_http: true, allow_private_targets: false };
        for raw in ["http://127.0.0.1/v1", "http://[::1]/v1", "http://169.254.169.254/latest/meta-data", "http://10.0.0.1:8080/"] {
            let u = normalize(raw, &p).unwrap();
            assert!(matches!(resolve_pinned(&u, &p).await, Err(UrlError::ForbiddenAddress(_))), "{raw}");
        }
        // localhost resolves to loopback and must be rejected too.
        let u = normalize("http://localhost/v1", &p).unwrap();
        assert!(matches!(resolve_pinned(&u, &p).await, Err(UrlError::ForbiddenAddress(_))));
    }

    proptest! {
        #[test]
        fn normalize_never_panics(s in "\\PC{0,200}") {
            let _ = normalize(&s, &Policy { allow_http: true, allow_private_targets: true });
        }

        #[test]
        fn within_prefix_never_panics(prefix in "\\PC{0,80}", target in "\\PC{0,80}") {
            if let Ok(u) = normalize(&target, &Policy { allow_http: true, allow_private_targets: true }) {
                let _ = within_prefix(&prefix, &u);
            }
        }

        #[test]
        fn path_prefix_requires_segment_boundary(seg in "[a-z]{1,8}", tail in "[a-z]{1,8}") {
            let p = Policy { allow_http: false, allow_private_targets: false };
            let prefix = format!("https://h.test/{seg}");
            let inside = normalize(&format!("https://h.test/{seg}/{tail}"), &p).unwrap();
            let outside = normalize(&format!("https://h.test/{seg}{tail}"), &p).unwrap();
            prop_assert!(within_prefix(&prefix, &inside).is_ok());
            prop_assert!(within_prefix(&prefix, &outside).is_err());
        }
    }
}
