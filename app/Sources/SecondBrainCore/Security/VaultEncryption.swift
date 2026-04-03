import Foundation
import CryptoKit

public enum VaultEncryption {
    /// Encrypt data using AES-256-GCM with a passphrase-derived key.
    public static func encrypt(data: Data, passphrase: String) throws -> Data {
        let key = deriveKey(from: passphrase)
        let sealedBox = try AES.GCM.seal(data, using: key)
        guard let combined = sealedBox.combined else {
            throw EncryptionError.sealFailed
        }
        return combined
    }

    /// Decrypt AES-256-GCM encrypted data.
    public static func decrypt(data: Data, passphrase: String) throws -> Data {
        let key = deriveKey(from: passphrase)
        let sealedBox = try AES.GCM.SealedBox(combined: data)
        return try AES.GCM.open(sealedBox, using: key)
    }

    /// Encrypt a file in place.
    public static func encryptFile(at url: URL, passphrase: String) throws {
        let plaintext = try Data(contentsOf: url)
        let encrypted = try encrypt(data: plaintext, passphrase: passphrase)
        try encrypted.write(to: url)
    }

    /// Decrypt a file in place.
    public static func decryptFile(at url: URL, passphrase: String) throws {
        let encrypted = try Data(contentsOf: url)
        let plaintext = try decrypt(data: encrypted, passphrase: passphrase)
        try plaintext.write(to: url)
    }

    /// Derive a 256-bit key from a passphrase using SHA256.
    private static func deriveKey(from passphrase: String) -> SymmetricKey {
        let hash = SHA256.hash(data: Data(passphrase.utf8))
        return SymmetricKey(data: hash)
    }

    public enum EncryptionError: Error, LocalizedError {
        case sealFailed
        case invalidData

        public var errorDescription: String? {
            switch self {
            case .sealFailed: return "Failed to encrypt data"
            case .invalidData: return "Invalid encrypted data"
            }
        }
    }
}
