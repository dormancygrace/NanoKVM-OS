// Only the AES cipher (with its MD5/EvpKDF/Base64/OpenSSL-format dependencies)
// is needed; importing the crypto-js root would bundle every algorithm.
import AES from 'crypto-js/aes';

// This key is only used to prevent the data from being transmitted in plaintext.
const SECRET_KEY = 'nanokvm-sipeed-2024';

export function encrypt(data: string) {
  const dataEncrypt = AES.encrypt(data, SECRET_KEY).toString();
  return encodeURIComponent(dataEncrypt);
}
