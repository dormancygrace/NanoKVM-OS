// SPDX-License-Identifier: GPL-3.0-or-later
// NanoKVM process adapter for OpenVPN 3 Core. No shell or imported scripts.
#define OPENVPN_CORE_API_VISIBILITY_HIDDEN
#include <client/ovpncli.cpp>
#include <openvpn/common/version.hpp>
#include <csignal>
#include <fstream>
#include <future>
#include <chrono>
#include <sys/stat.h>
#include <unistd.h>

static volatile std::sig_atomic_t stopping = 0;
static void on_signal(int) { stopping = 1; }
static std::string read_file(const std::string &path, size_t limit) {
    std::ifstream in(path, std::ios::binary);
    if (!in) throw std::runtime_error("cannot read input");
    std::string value((std::istreambuf_iterator<char>(in)), {});
    if (value.size() > limit) throw std::runtime_error("input too large");
    return value;
}
static std::string trim_line(std::string value) {
    while (!value.empty() && (value.back() == '\r' || value.back() == '\n')) value.pop_back();
    return value;
}
class Client : public openvpn::ClientAPI::OpenVPNClient {
public:
    std::string output, state="connecting", failure, address;
    bool dco=false;
    uint64_t received=0, sent=0;
    void snapshot() {
        if (output.empty()) return;
        std::ofstream file(output+".tmp", std::ios::trunc);
        file << "{\"state\":\"" << state << "\",\"error\":\"" << failure
             << "\",\"address\":\"" << address << "\",\"dco\":" << (dco?"true":"false")
             << ",\"received\":" << received << ",\"sent\":" << sent << "}\n";
        file.close();
        if (file) ::rename((output+".tmp").c_str(), output.c_str());
    }
    void log(const openvpn::ClientAPI::LogInfo &) override {} // Keys, server text and credentials never enter API logs.
    void event(const openvpn::ClientAPI::Event &e) override {
        if (e.name=="CONNECTED") {
            state="connected"; failure.clear();
            auto info=connection_info(); address=info.vpnIp4.empty()?info.vpnIp6:info.vpnIp4;
            if (address.find_first_not_of("0123456789abcdefABCDEF:.")!=std::string::npos) address.clear();
        } else if (e.name=="RECONNECTING") { state="reconnecting"; address.clear(); }
        else if (e.name=="AUTH_PENDING") state="authenticating";
        else if (e.name=="DISCONNECTED") { if (failure.empty()) state="off"; address.clear(); }
        if (e.fatal || e.name=="AUTH_FAILED" || e.name=="DYNAMIC_CHALLENGE") {
            state="error";
            failure=e.name=="AUTH_FAILED"?"VPN authentication failed":
                    e.name=="DYNAMIC_CHALLENGE"?"Interactive authentication is not supported by this interface":
                    "OpenVPN connection failed";
        }
        snapshot();
    }
    void acc_event(const openvpn::ClientAPI::AppCustomControlMessageEvent &) override {}
    bool pause_on_connection_timeout() override { return false; }
    void external_pki_cert_request(openvpn::ClientAPI::ExternalPKICertRequest &r) override { r.error=true; r.errorText="Upload the client certificate and key"; }
    void external_pki_sign_request(openvpn::ClientAPI::ExternalPKISignRequest &r) override { r.error=true; r.errorText="External signing is not supported"; }
    void clock_tick() override {
        // DCO statistics trigger netlink requests. Event callbacks can run before
        // the peer exists or during teardown, so never query from snapshot().
        if (state == "connected") {
            const auto stats=transport_stats();
            received=stats.bytesIn; sent=stats.bytesOut;
        }
        snapshot();
    }
};
int main(int argc,char **argv) {
    ::umask(0077);
    if (argc==2 && std::string(argv[1])=="--version") { std::cout << "OpenVPN 3 Core " << OPENVPN_VERSION << " (NanoKVM)\n"; return 0; }
    bool check=false;
    std::string config, auth, pass, status;
    for (int i=1;i<argc;++i) {
        std::string arg=argv[i];
        if (arg=="--check") { check=true; continue; }
        if (i+1==argc) return 2;
        if (arg=="--config") config=argv[++i];
        else if (arg=="--auth") auth=argv[++i];
        else if (arg=="--passphrase") pass=argv[++i];
        else if (arg=="--status") status=argv[++i];
        else return 2;
    }
    Client client;
    client.output=status;
    try {
        openvpn::ClientAPI::Config cfg;
        cfg.content=read_file(config,256*1024);
        cfg.guiVersion="NanoKVM OS";
        cfg.clockTickMS=1000;
        cfg.connTimeout=60;
        cfg.retryOnAuthFailed=false;
        cfg.googleDnsFallback=false;
        cfg.disableClientCert=cfg.content.find("<cert>")==std::string::npos;
        cfg.dco=true;
        if (!pass.empty()) cfg.privateKeyPassword=trim_line(read_file(pass,4098));
        auto eval=client.eval_config(cfg);
        if (eval.error) { client.state="error"; client.failure="Profile is not compatible with OpenVPN 3"; client.snapshot(); return 3; }
        if (check) return 0;
        client.dco=eval.dcoCompatible && openvpn::DCOTransport::OvpnDcoClient::available(nullptr);
        if (!auth.empty()) {
            std::istringstream credentials(read_file(auth,8196));
            openvpn::ClientAPI::ProvideCreds creds;
            std::getline(credentials,creds.username);std::getline(credentials,creds.password);
            if (client.provide_creds(creds).error) return 4;
        }
        std::signal(SIGTERM,on_signal); std::signal(SIGINT,on_signal); std::signal(SIGPIPE,SIG_IGN);
        // A supervisor may spawn us with termination signals masked. Installing
        // handlers alone does not unblock them; shutdown must reach clock_tick.
        sigset_t termination;
        sigemptyset(&termination); sigaddset(&termination,SIGTERM); sigaddset(&termination,SIGINT);
        if (sigprocmask(SIG_UNBLOCK,&termination,nullptr)) throw std::runtime_error("signal setup failed");
        client.snapshot();
        // Core intentionally masks signals inside connect(): its caller must
        // keep a parent thread available to request asynchronous shutdown.
        auto connection=std::async(std::launch::async,[&client] { return client.connect(); });
        bool stopRequested=false;
        while (connection.wait_for(std::chrono::milliseconds(100)) != std::future_status::ready) {
            if (stopping && !stopRequested) { client.stop(); stopRequested=true; }
        }
        auto result=connection.get();
        if (result.error) { client.state="error"; if (client.failure.empty()) client.failure="OpenVPN connection failed"; }
        client.snapshot();
        return result.error?1:0;
    } catch (...) { client.state="error"; client.failure="OpenVPN could not start"; client.snapshot(); return 2; }
}
