package uagen

// endpoint bases, vars so tests can point them at httptest servers.
var (
	epChromiumDash = "https://chromiumdash.appspot.com/fetch_releases"
	epChromeCFT    = "https://googlechromelabs.github.io/chrome-for-testing/last-known-good-versions.json"
	epFirefox      = "https://product-details.mozilla.org/1.0/firefox_versions.json"
	epFirefoxMob   = "https://product-details.mozilla.org/1.0/mobile_versions.json"
	epEdge         = "https://edgeupdates.microsoft.com/api/products"
	epEOL          = "https://endoflife.date/api/"
	epMDN          = "https://raw.githubusercontent.com/mdn/browser-compat-data/main/browsers/"
	epStatcounter  = "https://gs.statcounter.com"
	epWimb         = "https://www.whatismybrowser.com/guides/the-latest-user-agent/"
	epUACore       = "https://raw.githubusercontent.com/ua-parser/uap-core/master/tests/test_ua.yaml"
)
