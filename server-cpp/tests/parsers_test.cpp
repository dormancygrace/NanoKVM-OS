// Untrusted input and file permissions: SSH key import (formats, fingerprints, the RSA floor, all-or-nothing, the
// target file's safety and modes), TLS certificate pairs, multipart bodies, user disk paths and downloaded image
// names. Ported from the Go server's internal/sshkeys/import_test.go, utils/certstore_test.go,
// service/storage/userdisk_files_test.go and service/download/filename_test.go; the multipart cases cover the parser
// that replaces Go's mime/multipart. Keys and certificates come from ssh-keygen and openssl (run.sh), the expected
// fingerprints from ssh-keygen -l.

#include "check.hpp"

#include "api/multipart.hpp"
#include "common/log.hpp"
#include "storage/internal.hpp"
#include "system/ssh_keys.hpp"
#include "system/tls_x509.hpp"

#include <filesystem>
#include <fstream>
#include <sstream>
#include <sys/stat.h>
#include <unistd.h>

using namespace nkos;
namespace fs = std::filesystem;

namespace {

std::string dir;  // fixtures

std::string read(const fs::path &path)
{
	std::ifstream in(path, std::ios::binary);
	std::stringstream s;
	s << in.rdbuf();
	return s.str();
}
void write(const fs::path &path, const std::string &data) { std::ofstream(path, std::ios::binary) << data; }
std::string first_line(const fs::path &path)
{
	std::string s = read(path);
	return s.substr(0, s.find('\n'));
}
// "type base64" without the comment.
std::string bare(const std::string &line) { return line.substr(0, line.find(' ', line.find(' ') + 1)); }
fs::path temp(const char *name)
{
	std::string d = fs::temp_directory_path() / (std::string("nkos-") + name + "-XXXXXX");
	return ::mkdtemp(d.data()) ? d : "";
}
unsigned mode(const fs::path &p)
{
	struct stat st{};
	return ::stat(p.c_str(), &st) == 0 ? st.st_mode & 0777 : 0;
}

void ssh_tests()
{
	using system::ssh_keys::Outcome;
	system::ssh_keys::Result r;
	std::string e;
	for (const char *k : {"ed25519256", "ecdsa256", "ecdsa384", "ecdsa521", "rsa1024", "rsa2048"}) {
		std::string line = first_line(dir + "/" + k + ".pub");
		auto p = system::ssh_keys::parse_authorized_line(line);
		CHECK(p && system::ssh_keys::fingerprint(p->key) == first_line(dir + "/" + k + ".fp"), k);
		fs::path home = temp("ssh");
		Outcome o = system::ssh_keys::import_keys(home, line, r, e);
		CHECK(o == (std::string(k) == "rsa1024" ? Outcome::invalid : Outcome::ok), k);
		fs::remove_all(home);
	}

	// Existing lines (restrictions included) stay; keys already there are skipped; the modes are set.
	const std::string a = bare(first_line(dir + "/ed25519256.pub")), b = bare(first_line(dir + "/other.pub"));
	fs::path home = temp("ssh"), ssh = home / ".ssh", keys = ssh / "authorized_keys";
	fs::create_directory(ssh);
	fs::permissions(ssh, fs::perms(0755));
	const std::string original = "# Keep this comment\nfrom=\"192.0.2.1\",no-pty " + a + " restricted";
	write(keys, original);
	fs::permissions(keys, fs::perms(0644));
	r = {};
	Outcome o = system::ssh_keys::import_keys(home, "# new keys\r\n" + a + " different comment\r\n" + b + " laptop\r\n" + b + " duplicate\n", r, e);
	CHECK(o == Outcome::ok && r.added == 1 && r.skipped == 2 && r.fingerprints.size() == 1, e);
	CHECK(read(keys) == original + "\n" + b + " laptop\n", "existing access changed");
	CHECK(mode(ssh) == 0700 && mode(keys) == 0600, "modes");
	r = {};
	CHECK(system::ssh_keys::import_keys(home, b, r, e) == Outcome::ok && r.added == 0 && r.skipped == 1);
	CHECK(read(keys) == original + "\n" + b + " laptop\n", "a duplicate changed the file");
	fs::remove_all(home);

	// Invalid input is refused whole, before the file system is touched.
	std::string many;
	for (int i = 0; i < 101; i++) many += a + "\n";
	for (const std::string &input : {std::string(), std::string("# comment"), std::string("garbage"), a + "\ninvalid",
	                                 std::string("-----BEGIN OPENSSH PRIVATE KEY-----\nabc"), "command=\"id\" " + a,
	                                 a + std::string(1, '\0'), a + "\x1b", std::string("\xff"),
	                                 std::string(system::ssh_keys::max_input + 1, 'a'), many}) {
		fs::path h = temp("ssh");
		CHECK(system::ssh_keys::import_keys(h, input, r, e) == Outcome::invalid && !fs::exists(h / ".ssh"), input.substr(0, 40));
		fs::remove_all(h);
	}

	// Symbolic links and other files in the way are refused; nothing outside changes.
	for (const std::string kind : {"home-link", "dir-link", "file-link", "directory", "fifo", "oversized"}) {
		fs::path h = temp("ssh"), out = temp("out"), links = temp("lnk");
		write(out / "outside", "unchanged");
		fs::path target = h;
		if (kind == "home-link") fs::create_directory_symlink(h, target = links / "home");
		else if (kind == "dir-link") fs::create_directory_symlink(out, h / ".ssh");
		else {
			fs::create_directory(h / ".ssh");
			fs::path p = h / ".ssh" / "authorized_keys";
			if (kind == "file-link") fs::create_symlink(out / "outside", p);
			if (kind == "directory") fs::create_directory(p);
			if (kind == "fifo") ::mkfifo(p.c_str(), 0600);
			if (kind == "oversized") write(p, std::string(256 * 1024 + 1, 'x'));
		}
		CHECK(system::ssh_keys::import_keys(target, a, r, e) != Outcome::ok, kind);
		CHECK(read(out / "outside") == "unchanged", kind);
		for (auto &d : {h, out, links}) fs::remove_all(d);
	}
}

void tls_tests()
{
	using system::tls::parse_certificate_pair;
	const std::string cert = read(dir + "/leaf.crt"), key = read(dir + "/leaf.key"), issuer = read(dir + "/issuer.crt"),
	                  other = read(dir + "/other.key");
	auto p = parse_certificate_pair(cert, key);
	CHECK(p && p->pair.leaf.subject_cn == "leaf.example" && p->cert_out == cert && p->key_out == key, p ? "" : p.error());
	std::string combined = "# comment before\n" + key + cert;
	p = parse_certificate_pair(combined, combined);
	CHECK(p && p->cert_out.find("PRIVATE KEY") == std::string::npos && p->key_out.find("CERTIFICATE") == std::string::npos &&
	          p->key_out.find("PRIVATE KEY") != std::string::npos, "a combined file is split");
	p = parse_certificate_pair(cert + issuer, key);
	CHECK(p && p->pair.chain.size() == 2, "the chain is kept");
	std::string chain = cert;
	for (int i = 0; i < 8; i++) chain += issuer;
	p = parse_certificate_pair(chain, key);
	CHECK(!p && p.error().find("more than") != std::string::npos, "a long chain");
	const std::string encrypted = "-----BEGIN ENCRYPTED PRIVATE KEY-----\nMIIB\n-----END ENCRYPTED PRIVATE KEY-----\n",
	                  legacy = "-----BEGIN EC PRIVATE KEY-----\nProc-Type: 4,ENCRYPTED\nDEK-Info: AES-256-CBC,00\n\nMIIB\n-----END EC PRIVATE KEY-----\n";
	struct {
		const char *name;
		std::string cert, key;
		const char *want;
	} bad[] = {{"mismatch", cert, other, "does not match"}, {"no certificate", key, key, "no certificate"},
	           {"no key", cert, cert, "no private key"}, {"encrypted", cert, encrypted, "password-protected"},
	           {"legacy encrypted", cert, legacy, "password-protected"}, {"garbage", "hello", key, "no certificate"},
	           {"empty", "", "", "no certificate"}};
	for (auto &c : bad) {
		p = parse_certificate_pair(c.cert, c.key);
		CHECK(!p && p.error().find(c.want) != std::string::npos, std::string(c.name) + ": " + (p ? "accepted" : p.error()));
	}
}

void multipart_tests()
{
	auto part = [](const std::string &disposition, const std::string &data) {
		return "--XyZ\r\nContent-Disposition: form-data; " + disposition + "\r\n\r\n" + data + "\r\n";
	};
	const std::string body = part(R"(name="file"; filename="../../etc/passwd")", "DATA") + part(R"(name="image"; filename="/abs/dir/x.iso")", "") +
	                         part(R"(name="note")", "hello") + "--XyZ--\r\n";
	auto parts = api::multipart("multipart/form-data; boundary=XyZ", body);
	const api::Part *file = parts ? api::form_file(*parts, "file") : nullptr;
	CHECK(file && file->filename == "passwd" && file->data == "DATA", "a file name is its base");
	CHECK(parts && api::form_file(*parts, "image") && api::form_file(*parts, "image")->filename == "x.iso");
	CHECK(parts && api::form_value(*parts, "note") == "hello" && !api::form_file(*parts, "note"));
	CHECK(!api::multipart("text/plain; boundary=XyZ", body) && !api::multipart("multipart/form-data", body) &&
	      !api::multipart("multipart/form-data; boundary=XyZ", body.substr(0, body.size() - 9)), "malformed bodies");
}

void name_tests()
{
	std::string name;
	for (const std::string &bad : std::vector<std::string>{"../secret", "foo/../secret", "/etc/passwd", "a//b", "a/./b", "x\\y", std::string("a\0b", 3), "a:",
	                              "file.", "file ", std::string(256, 'a'), "\xff"})
		CHECK(storage::user_disk_path(bad, name), "user disk path accepted: " + bad);
	for (const std::string good : {"", ".", "folder/BIOS 1.bin", "Папка/配置.txt"})
		CHECK(!storage::user_disk_path(good, name), "user disk path rejected: " + good);
	for (const std::string good : {"debian-13.iso", "disk.IMG", "rescue_1.img"})
		CHECK(!storage::validate_image_filename(good), "image name rejected: " + good);
	for (const std::string bad : {"", "..", "..iso", ".jwt_secret", "server.key", "a b.iso", "notes.txt", "x.iso/", "../a.iso"})
		CHECK(storage::validate_image_filename(bad), "image name accepted: " + bad);
}

} // namespace

int main(int argc, char **argv)
{
	if (argc < 2) return 2;
	dir = argv[1];
	log::set_level("error");
	ssh_tests();
	tls_tests();
	multipart_tests();
	name_tests();
	return finish("parsers");
}
