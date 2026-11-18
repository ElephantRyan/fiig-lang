package dev.fiig.intellij

import com.intellij.openapi.project.Project
import com.intellij.openapi.vfs.VirtualFile
import com.jetbrains.jsonSchema.extension.JsonSchemaFileProvider
import com.jetbrains.jsonSchema.extension.JsonSchemaProviderFactory
import com.jetbrains.jsonSchema.extension.SchemaType

/**
 * ADR 0090 §6: completion and validation in a package's manifest and its
 * capabilities files. The schemas are docs/schemas/, copied in at build.
 */
class FiigJsonSchemas : JsonSchemaProviderFactory {
    override fun getProviders(project: Project): List<JsonSchemaFileProvider> = listOf(
        Provider("fiig package manifest", "/schemas/fiig.pkg.schema.json") { it.name == "fiig.pkg.json" },
        Provider("fiig capabilities file", "/schemas/capabilities.schema.json") { isCapabilitiesFile(it.name) },
    )

    private class Provider(private val title: String, private val resource: String, private val matches: (VirtualFile) -> Boolean) : JsonSchemaFileProvider {
        override fun isAvailable(file: VirtualFile): Boolean = matches(file)
        override fun getName(): String = title
        override fun getSchemaFile(): VirtualFile? = JsonSchemaProviderFactory.getResourceFile(FiigJsonSchemas::class.java, resource)
        override fun getSchemaType(): SchemaType = SchemaType.embeddedSchema
    }

    companion object {
        private val CAPS = Regex("^(caps|capabilities)(\\.[^.]+)*\\.json$")

        /** Kept in step with tooling/intellijgen's capsFileName. */
        fun isCapabilitiesFile(name: String): Boolean = CAPS.matches(name)
    }
}
