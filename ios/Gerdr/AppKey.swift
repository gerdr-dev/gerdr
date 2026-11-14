import CryptoKit
import Foundation
import LocalAuthentication
import Security

/// This device's App key: re-verify with Face ID instead of the browser (server: internal/auth/appkey.go, which has
/// the rules and why; docs/dev/IOS.md "App key").
///
/// A P-256 key made in the Secure Enclave: the private half never leaves it, every signature needs Face ID / Touch ID,
/// and enrolling a new face or finger voids it (`.biometryCurrentSet`; then the browser approval makes a new one).
/// What the Secure Enclave hands back is a blob only this phone's enclave can use; it's kept in the Keychain, this
/// device only, out of backups. One key per server origin.
enum AppKey {
    /// Secure Enclave present (not in the Simulator) and Face ID / Touch ID set up.
    static var available: Bool {
        var err: NSError?
        return SecureEnclave.isAvailable && LAContext().canEvaluatePolicy(.deviceOwnerAuthenticationWithBiometrics, error: &err)
    }

    private static func account(_ origin: String) -> String { "appkey:" + origin }
    private static let service = "io.github.zbysir.gerdr.appkey"

    /// A fresh key for this origin (replacing any old one); returns its public half, uncompressed (65 bytes).
    static func create(origin: String) throws -> Data {
        var err: Unmanaged<CFError>?
        guard let ac = SecAccessControlCreateWithFlags(nil, kSecAttrAccessibleWhenUnlockedThisDeviceOnly,
                                                       [.privateKeyUsage, .biometryCurrentSet], &err) else {
            throw err!.takeRetainedValue() as Error
        }
        let key = try SecureEnclave.P256.Signing.PrivateKey(accessControl: ac)
        delete(origin: origin)
        let add: [String: Any] = [
            kSecClass as String: kSecClassGenericPassword,
            kSecAttrService as String: service,
            kSecAttrAccount as String: account(origin),
            kSecAttrAccessible as String: kSecAttrAccessibleWhenUnlockedThisDeviceOnly,
            kSecValueData as String: key.dataRepresentation,
        ]
        let st = SecItemAdd(add as CFDictionary, nil)
        guard st == errSecSuccess else { throw NSError(domain: NSOSStatusErrorDomain, code: Int(st)) }
        return key.publicKey.x963Representation
    }

    static func exists(origin: String) -> Bool { blob(origin) != nil }

    static func delete(origin: String) {
        let q: [String: Any] = [kSecClass as String: kSecClassGenericPassword, kSecAttrService as String: service,
                                kSecAttrAccount as String: account(origin)]
        SecItemDelete(q as CFDictionary)
    }

    private static func blob(_ origin: String) -> Data? {
        let q: [String: Any] = [kSecClass as String: kSecClassGenericPassword, kSecAttrService as String: service,
                                kSecAttrAccount as String: account(origin), kSecReturnData as String: true]
        var out: CFTypeRef?
        return SecItemCopyMatching(q as CFDictionary, &out) == errSecSuccess ? out as? Data : nil
    }

    /// The server's payload (auth.AppKeyPayload), signed after Face ID; DER, as the server's VerifyASN1 wants.
    /// Throws LAError.userCancel when the person dismisses the prompt.
    static func sign(origin: String, host: String, deviceID: String, nonce: String) throws -> Data {
        guard let b = blob(origin) else { throw NSError(domain: "AppKey", code: 1) }
        let ctx = LAContext()
        ctx.localizedReason = L("验证这台设备", "Verify this device")
        let key = try SecureEnclave.P256.Signing.PrivateKey(dataRepresentation: b, authenticationContext: ctx)
        let payload = Data("gerdr-appkey-v1\n\(host)\n\(deviceID)\n\(nonce)".utf8)
        return try key.signature(for: payload).derRepresentation
    }
}
