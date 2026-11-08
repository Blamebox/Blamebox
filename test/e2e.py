#!/usr/bin/env python3
"""End-to-end test: the real probes against a simulated home network.

Runs inside an isolated network namespace (needs root):

    sudo unshare -n python3 test/e2e.py ./blamebox ./fakenet

It starts fakenet and BlameBox on loopback addresses, breaks the network with iptables
(ISP outage, router DNS outage, router down, packet loss) and checks what BlameBox concluded.
"""
import fcntl, json, socket, struct, subprocess, sys, tempfile, time, urllib.request

BB, FAKENET = sys.argv[1], sys.argv[2]
INTERVAL = 5  # seconds between checks


def lo_up():
    s = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
    ifr = struct.pack("16sH14s", b"lo", 0, b"\0" * 14)
    flags = struct.unpack("16sH14s", fcntl.ioctl(s, 0x8913, ifr))[1]
    fcntl.ioctl(s, 0x8914, struct.pack("16sH14s", b"lo", flags | 1, b"\0" * 14))


def ipt(*rules):
    subprocess.run(["iptables", "-F", "OUTPUT"], check=True)
    for r in rules:
        subprocess.run(["iptables", "-A", "OUTPUT"] + r.split(), check=True)


def api(path):
    with urllib.request.urlopen("http://127.0.0.1:8088" + path, timeout=5) as r:
        return json.load(r)


GW, NET, WEB = "127.0.0.2", ["127.0.0.3", "127.0.0.4"], "127.0.0.5"
SCENARIO = [
    ("healthy", []),
    ("isp", [f"-d {ip} -j DROP" for ip in NET + [WEB]] + [f"-d {GW} -p udp --dport 53 -j DROP"]),
    ("healthy", []),
    ("dns_isp", [f"-d {GW} -p udp --dport 53 -j DROP", f"-d {GW} -p tcp --dport 53 -j DROP"]),
    ("healthy", []),
    ("lan", [f"-d {ip} -j DROP" for ip in [GW] + NET + [WEB]]),
    ("healthy", []),
]
PHASE = 40  # seconds per phase

lo_up()
data = tempfile.mkdtemp()
env = {
    "PATH": "/usr/sbin:/usr/bin:/sbin:/bin", "TZ": "UTC",
    "BLAMEBOX_LISTEN": "127.0.0.1:8088", "BLAMEBOX_DATA_DIR": data, "BLAMEBOX_INTERVAL": str(INTERVAL),
    "BLAMEBOX_GATEWAY": GW, "BLAMEBOX_TARGETS": ",".join(NET), "BLAMEBOX_DNS_PUBLIC": ",".join(NET),
    "BLAMEBOX_WEB_URLS": "http://www.cloudflare.com/cdn-cgi/trace,http://www.gstatic.com/generate_204",
}
fake = subprocess.Popen([FAKENET])
time.sleep(0.5)
bb = subprocess.Popen([BB], env=env, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, text=True)
failures = []
try:
    time.sleep(2)
    marks = []
    for name, rules in SCENARIO:
        ipt(*rules)
        marks.append((name, time.time()))
        mid_checked = False
        end = time.time() + PHASE
        while time.time() < end:
            time.sleep(1)
            if not mid_checked and time.time() > end - PHASE / 2:
                mid_checked = True
                st = api("/api/status")
                got = st["diag"]["c"] if st["diag"]["s"] == "down" else ("healthy" if st["diag"]["s"] == "ok" else st["diag"]["c"])
                ok = got == name
                print(f"{name:8s} → live verdict: {st['diag']['s']}/{st['diag']['c']} layers={st['diag']['l']} {'OK' if ok else 'MISMATCH'}")
                if not ok:
                    failures.append(f"{name}: live verdict {got}")
    ipt()
    time.sleep(2 * INTERVAL)
    incs = sorted(api("/api/incidents?days=1"), key=lambda i: i["start"])
    outages = [i for i in incs if i["kind"] == "outage"]
    print("\nIncidents recorded:")
    for i in outages:
        print(f"  {i['cause']:8s} {i['duration_s']:5.0f} s  cycles={i['cycles']}  router answered={i['evidence']['gw_ok']}  ongoing={i['ongoing']}")
    want = [n for n, r in SCENARIO if n != "healthy"]
    if [i["cause"] for i in outages] != want:
        failures.append(f"incident causes {[i['cause'] for i in outages]} != {want}")
    for i in outages:
        if not (PHASE - 3 * INTERVAL <= i["duration_s"] <= PHASE + 3 * INTERVAL):
            failures.append(f"{i['cause']} lasted {i['duration_s']} s, expected about {PHASE} s")
        if i["cause"] == "isp" and i["evidence"]["gw_ok"] != i["cycles"]:
            failures.append("during the ISP outage the router answered every time: evidence should say so")
    # Packet loss to the Internet only: a degradation must be detected.
    ipt(*[f"-d {ip} -p icmp -m statistic --mode random --probability 0.3 -j DROP" for ip in NET])
    deadline = time.time() + 120
    seen = False
    while time.time() < deadline:
        time.sleep(3)
        st = api("/api/status")
        if st["diag"]["c"] == "loss_isp":
            seen = True
            print(f"\nloss     → live verdict: {st['diag']['s']}/{st['diag']['c']} window loss {st['window']['net_loss']:.0%} OK")
            break
    if not seen:
        failures.append("30 % packet loss to the Internet was not detected within 2 minutes")
    report = urllib.request.urlopen("http://127.0.0.1:8088/report?days=1", timeout=5).read().decode()
    # The router stayed up during the ISP-side outages only: the report may say so for those,
    # and must put the router outage on the home side.
    if "liés au domicile" not in report or "Domicile" not in report:
        failures.append("the report must attribute the router outage to the home side")
finally:
    ipt()
    bb.terminate()
    fake.terminate()
    out = bb.communicate(timeout=5)[0]
    print("\n--- blamebox log ---\n" + out[-1500:])

if failures:
    print("\nFAILED:\n- " + "\n- ".join(failures))
    sys.exit(1)
print("\nALL END-TO-END CHECKS PASSED")
