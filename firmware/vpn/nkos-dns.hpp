// SPDX-License-Identifier: GPL-3.0-or-later
#pragma once
#include <openvpn/common/action.hpp>
#include <openvpn/client/dns_options.hpp>
#include <sys/wait.h>
#include <unistd.h>
#include <signal.h>
#include <fcntl.h>

namespace openvpn {
class NkosDNS : public Action {
    std::string content;
    std::shared_ptr<unsigned long> generation;
    bool teardown;
    inline static unsigned long active=0, sequence=0;
public:
    explicit NkosDNS(std::string value, std::shared_ptr<unsigned long> token, bool down)
        :content(std::move(value)),generation(std::move(token)),teardown(down) {}
    std::string to_string() const override { return "NanoKVM VPN DNS"; }
    void execute(std::ostream &) override {
        // Core can establish new TUN routes before tearing down the old set.
        // An old disconnect must not erase the replacement connection's DNS.
        if (teardown && active!=*generation) return;
        int fd[2];
        if (pipe2(fd,O_CLOEXEC)) throw Exception("resolver pipe failed");
        const bool remove=content.empty();
        pid_t pid=fork();
        if (pid<0) {close(fd[0]);close(fd[1]);throw Exception("resolver fork failed");}
        if (!pid) {
            dup2(fd[0],STDIN_FILENO);close(fd[0]);close(fd[1]);
            if (remove) execl("/usr/sbin/resolvconf","resolvconf","-d","nkos.openvpn","-f",nullptr);
            else execl("/usr/sbin/resolvconf","resolvconf","-a","nkos.openvpn","-m","0","-x",nullptr);
            _exit(127);
        }
        close(fd[0]);
        if (!content.empty()) { ssize_t ignored=write(fd[1],content.data(),content.size()); (void)ignored; }
        close(fd[1]);
        int result=0;
        for (int i=0;i<100;++i) {
            if (waitpid(pid,&result,WNOHANG)==pid) {
                if (!WIFEXITED(result)||WEXITSTATUS(result)) throw Exception("resolver update failed");
                if (teardown) active=0;
                else active=*generation=++sequence;
                return;
            }
            usleep(50000);
        }
        kill(pid,SIGKILL);waitpid(pid,&result,0);
        throw Exception("resolver update timed out");
    }
    static void configure(const DnsOptions &dns,ActionList &create,ActionList &destroy) {
        std::string data;
        unsigned count=0;
        for (const auto &[priority,server]:dns.servers) {
            (void)priority;
            for (const auto &addr:server.addresses) {
                if (++count>3) break;
                if (addr.port!=0 && addr.port!=53) throw Exception("custom DNS port is not supported");
                if (addr.address.find_first_not_of("0123456789abcdefABCDEF:.")!=std::string::npos) throw Exception("invalid DNS address");
                data+="nameserver "+addr.address+"\n";
            }
        }
        // openresolv provides a single system resolver; server DNS applies
        // globally while connected. Split-DNS policies are not emulated.
        if (!data.empty()) {
            std::string search;
            for (const auto &domain:dns.search_domains) {
                if (domain.domain.find_first_not_of("abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789.-")!=std::string::npos) throw Exception("invalid DNS domain");
                if (search.size()+domain.domain.size()+1>240) break;
                search+=" "+domain.domain;
            }
            if (!search.empty()) data+="search"+search+"\n";
        }
        auto token=std::make_shared<unsigned long>(0);
        create.add(new NkosDNS(data,token,false));destroy.add(new NkosDNS("",token,true));
    }
};
}
