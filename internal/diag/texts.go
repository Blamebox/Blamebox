package diag

import "github.com/blamebox/blamebox/internal/model"

// Text explains a cause in plain words.
type Text struct {
	Title  string `json:"title"`
	Body   string `json:"body"`
	Advice string `json:"advice,omitempty"`
	Short  string `json:"short"` // a few words, for lists and alerts
	Side   string `json:"side"`  // who is responsible, in a few words
}

var texts = map[string]map[model.Cause]Text{
	"fr": {
		model.CauseNone: {
			Title: "Tout fonctionne",
			Body:  "Ta box répond, Internet est joignable, les noms de sites sont traduits et le web répond.",
			Short: "Tout fonctionne", Side: "—",
		},
		model.CauseLAN: {
			Title:  "Ta box ne répond pas",
			Body:   "La sonde n'arrive plus à joindre ta box. Le problème est de ton côté : box éteinte ou plantée, câble débranché, ou souci sur l'appareil qui fait tourner BlameBox.",
			Advice: "Vérifie que la box est allumée et regarde ses voyants, puis le câble réseau de la sonde. Redémarre la box si besoin.",
			Short:  "Box injoignable", Side: "Domicile",
		},
		model.CauseISP: {
			Title:  "Panne côté opérateur",
			Body:   "Ta box répond normalement, mais aucun serveur Internet n'est joignable. Le problème se situe après ta box : sur la ligne ou chez ton opérateur.",
			Advice: "Inutile de toucher à ton installation. Regarde les voyants de la box et la page d'incidents de ton opérateur. BlameBox garde la preuve pour ton rapport.",
			Short:  "Coupure côté opérateur", Side: "Opérateur",
		},
		model.CauseDNSISP: {
			Title:  "Le DNS de ta box ne répond plus",
			Body:   "Internet répond, mais le DNS de ta box (l'annuaire qui traduit les noms de sites en adresses) ne répond plus. La connexion semble coupée alors qu'elle fonctionne.",
			Advice: "Contournement immédiat : règle un DNS public (par exemple 1.1.1.1 ou 9.9.9.9) sur ta box ou sur tes appareils.",
			Short:  "Panne DNS de l'opérateur", Side: "Opérateur",
		},
		model.CauseDNSAll: {
			Title:  "Aucun DNS ne répond",
			Body:   "Internet répond, mais aucun serveur DNS ne répond, ni celui de la box ni les DNS publics. Un filtrage ou un pare-feu peut en être la cause.",
			Advice: "Vérifie le contrôle parental et le pare-feu de ta box. Si rien n'a changé chez toi, contacte ton opérateur.",
			Short:  "DNS injoignables", Side: "Opérateur ou filtrage",
		},
		model.CauseWeb: {
			Title:  "Les sites web ne répondent pas",
			Body:   "Internet répond et les noms sont traduits, mais les requêtes web échouent : portail captif, filtrage, ou panne partielle du réseau de l'opérateur.",
			Advice: "Ouvre un site dans ton navigateur. Si une page de connexion s'affiche, c'est un portail captif.",
			Short:  "Web injoignable", Side: "Opérateur ou filtrage",
		},
		model.CauseLossLAN: {
			Title:  "Pertes de paquets avant la box",
			Body:   "Des paquets se perdent déjà entre la sonde et ta box. Le souci est dans ton réseau local : câble, Wi-Fi ou box surchargée.",
			Advice: "Branche la sonde en câble si elle est en Wi-Fi, change le câble ou redémarre la box.",
			Short:  "Pertes sur le réseau local", Side: "Domicile",
		},
		model.CauseLossISP: {
			Title:  "Pertes de paquets côté opérateur",
			Body:   "Ton réseau local est sain, mais une partie des paquets vers Internet se perd. C'est typique d'une ligne saturée ou défaillante en amont de ta box.",
			Advice: "Si ça revient surtout le soir, c'est un signe de saturation chez l'opérateur. Garde ces mesures pour ton rapport.",
			Short:  "Pertes côté opérateur", Side: "Opérateur",
		},
	},
	"en": {
		model.CauseNone: {
			Title: "Everything works",
			Body:  "Your router answers, the Internet is reachable, names resolve and the web responds.",
			Short: "All good", Side: "—",
		},
		model.CauseLAN: {
			Title:  "Your router is not answering",
			Body:   "The probe can no longer reach your router. The problem is on your side: router off or frozen, cable unplugged, or an issue with the device running BlameBox.",
			Advice: "Check that the router is on and look at its lights, then the probe's network cable. Restart the router if needed.",
			Short:  "Router unreachable", Side: "Home",
		},
		model.CauseISP: {
			Title:  "ISP-side outage",
			Body:   "Your router answers normally, but no Internet server is reachable. The problem is beyond your router: on the line or at your ISP.",
			Advice: "No need to touch your setup. Check the router lights and your ISP's status page. BlameBox keeps the evidence for your report.",
			Short:  "ISP-side outage", Side: "ISP",
		},
		model.CauseDNSISP: {
			Title:  "Your router's DNS stopped answering",
			Body:   "The Internet answers, but your router's DNS (the directory that turns site names into addresses) does not. The connection looks dead while it actually works.",
			Advice: "Quick fix: set a public DNS (for example 1.1.1.1 or 9.9.9.9) on your router or devices.",
			Short:  "ISP DNS outage", Side: "ISP",
		},
		model.CauseDNSAll: {
			Title:  "No DNS server answers",
			Body:   "The Internet answers, but no DNS server does, neither your router's nor public ones. Filtering or a firewall may be the cause.",
			Advice: "Check your router's parental control and firewall. If nothing changed on your side, contact your ISP.",
			Short:  "DNS unreachable", Side: "ISP or filtering",
		},
		model.CauseWeb: {
			Title:  "Websites are not responding",
			Body:   "The Internet answers and names resolve, but web requests fail: captive portal, filtering, or a partial outage on the ISP network.",
			Advice: "Open a site in your browser. If a login page shows up, it is a captive portal.",
			Short:  "Web unreachable", Side: "ISP or filtering",
		},
		model.CauseLossLAN: {
			Title:  "Packet loss before the router",
			Body:   "Packets are already lost between the probe and your router. The issue is on your local network: cable, Wi-Fi or an overloaded router.",
			Advice: "Wire the probe if it is on Wi-Fi, swap the cable or restart the router.",
			Short:  "Local network loss", Side: "Home",
		},
		model.CauseLossISP: {
			Title:  "ISP-side packet loss",
			Body:   "Your local network is healthy, but some packets to the Internet are lost. This is typical of a congested or faulty line beyond your router.",
			Advice: "If it mostly happens in the evening, it points to congestion at your ISP. Keep these measurements for your report.",
			Short:  "ISP-side loss", Side: "ISP",
		},
	},
}

// Explain returns the plain-words text for a cause.
func Explain(c model.Cause, lang string) Text {
	m, ok := texts[lang]
	if !ok {
		m = texts["fr"]
	}
	if t, ok := m[c]; ok {
		return t
	}
	return m[model.CauseNone]
}

// Langs lists the supported languages.
func Langs() []string { return []string{"fr", "en"} }
