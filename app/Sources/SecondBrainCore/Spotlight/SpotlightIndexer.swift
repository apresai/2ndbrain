import Foundation
import CoreSpotlight
import UniformTypeIdentifiers

public final class SpotlightIndexer: @unchecked Sendable {
    private let searchableIndex: CSSearchableIndex
    private let bundleIdentifier: String

    public init(bundleIdentifier: String = "com.apresai.secondbrain") {
        self.searchableIndex = CSSearchableIndex(name: bundleIdentifier)
        self.bundleIdentifier = bundleIdentifier
    }

    /// Index a single document in Spotlight.
    public func indexDocument(
        id: String,
        title: String,
        docType: String,
        tags: [String],
        bodyPreview: String,
        filePath: String,
        modifiedDate: Date
    ) {
        let attributeSet = CSSearchableItemAttributeSet(contentType: UTType.utf8PlainText)
        attributeSet.title = title
        attributeSet.contentDescription = String(bodyPreview.prefix(500))
        attributeSet.keywords = tags + [docType]
        attributeSet.contentModificationDate = modifiedDate
        attributeSet.path = filePath

        let item = CSSearchableItem(
            uniqueIdentifier: id,
            domainIdentifier: bundleIdentifier,
            attributeSet: attributeSet
        )
        item.expirationDate = Date.distantFuture

        searchableIndex.indexSearchableItems([item]) { error in
            if let error {
                print("Spotlight index error: \(error)")
            }
        }
    }

    /// Remove a document from Spotlight index.
    public func removeDocument(id: String) {
        searchableIndex.deleteSearchableItems(withIdentifiers: [id]) { error in
            if let error {
                print("Spotlight delete error: \(error)")
            }
        }
    }

    /// Index all documents from the vault manager.
    public func indexAll(vault: VaultManager) {
        let files = vault.listMarkdownFiles()
        var items: [CSSearchableItem] = []

        for url in files {
            guard let doc = try? FrontmatterParser.loadDocument(from: url) else { continue }

            let attributeSet = CSSearchableItemAttributeSet(contentType: UTType.utf8PlainText)
            attributeSet.title = doc.title
            attributeSet.contentDescription = String(doc.body.prefix(500))
            attributeSet.keywords = doc.tags + [doc.docType]
            attributeSet.contentModificationDate = doc.modifiedAt
            attributeSet.path = url.path

            let item = CSSearchableItem(
                uniqueIdentifier: doc.id,
                domainIdentifier: bundleIdentifier,
                attributeSet: attributeSet
            )
            item.expirationDate = Date.distantFuture
            items.append(item)
        }

        searchableIndex.indexSearchableItems(items) { error in
            if let error {
                print("Spotlight batch index error: \(error)")
            }
        }
    }

    /// Clear all Spotlight entries for this app.
    public func clearAll() {
        searchableIndex.deleteAllSearchableItems { error in
            if let error {
                print("Spotlight clear error: \(error)")
            }
        }
    }
}
