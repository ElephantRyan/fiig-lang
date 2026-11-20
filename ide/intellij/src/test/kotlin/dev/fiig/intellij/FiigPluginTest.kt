package dev.fiig.intellij

import com.intellij.codeInsight.highlighting.BraceMatchingUtil
import com.intellij.execution.lineMarker.RunLineMarkerContributor
import com.intellij.ide.fileTemplates.FileTemplateManager
import com.intellij.openapi.actionSystem.IdeActions
import com.intellij.openapi.fileTypes.FileTypeManager
import com.intellij.psi.util.elementType
import com.intellij.psi.util.siblings
import com.intellij.testFramework.fixtures.BasePlatformTestCase
import com.jetbrains.jsonSchema.extension.JsonSchemaProviderFactory
import java.io.File
import java.nio.file.Files
import java.util.concurrent.TimeUnit

/** ADR 0090 §7: the plugin in a test IDE. */
class FiigPluginTest : BasePlatformTestCase() {
    fun testFileType() {
        assertSame(FiigFileType, FileTypeManager.getInstance().getFileTypeByFileName("GetCustomer.fiig"))
    }

    // ADR 0108: an application's src/ holds only folders, so a new file made
    // directly there must start with one; a library's top level and a folder
    // are unaffected.
    fun testAnApplicationsSrcNeedsAFolder() {
        val application = """{"path": "t.app", "kind": "application", "version": "1.0.0"}"""
        val library = """{"path": "t.lib", "kind": "library", "version": "1.0.0"}"""
        assertNotNull(FiigNaming.topLevelProblem("src", application, "getOrder"))
        assertNull(FiigNaming.topLevelProblem("src", application, "orders/getOrder"))
        assertNull(FiigNaming.topLevelProblem("src", application, "orders\\getOrder"))
        assertNull(FiigNaming.topLevelProblem("src", application, ""))
        assertNull(FiigNaming.topLevelProblem("orders", application, "getOrder"))
        assertNull(FiigNaming.topLevelProblem("src", library, "getOrder"))
        assertNull(FiigNaming.topLevelProblem("src", null, "getOrder"))
    }

    // A file's icon is its declaration's kind; every kind has its own.
    fun testFileIconByKind() {
        val provider = FiigIconProvider()
        fun iconOf(text: String) = provider.getIcon(myFixture.configureByText("X.fiig", text), 0)
        assertSame(FiigIcons.forKind("rule"), iconOf("// a comment\nrule r(x: int) -> int { return x }\n"))
        assertSame(FiigIcons.forKind("model"), iconOf("model M {\n    a: int\n}\n"))
        assertSame(FiigIcons.forKind("adapter"), iconOf("adapter http api {\n}\n"))
        assertSame(FiigIcons.forKind("test"), iconOf("test t {\n}\n"))
        assertNull(iconOf("// nothing yet\n"))
        val icons = (FiigNaming.KINDS + "test").map(FiigIcons::forKind)
        assertEquals(icons.size, icons.toSet().size)
        assertFalse(icons.any { it === FiigIcons.FILE })
    }

    fun testHighlightingByTokenKind() {
        val h = FiigSyntaxHighlighter()
        assertSame(FiigColors.KEYWORD, h.getTokenHighlights(FiigTokens.KEYWORD).single())
        assertSame(FiigColors.TYPE_NAME, h.getTokenHighlights(FiigTokens.TYPE_NAME).single())
        assertSame(FiigColors.INVALID_ESCAPE, h.getTokenHighlights(FiigTokens.INVALID_ESCAPE).single())
        assertEquals(24, FiigColorSettingsPage().attributeDescriptors.size)
    }

    // ADR 0106 §1: every token type tooling/lsp/semantic.go sends has a
    // colour, and the settings page's demo tags name them.
    fun testEveryCallKindHasAColour() {
        val types = listOf("rule", "conversion", "activity", "gateway", "workflow", "formula", "helper", "capability")
        assertEquals(types.toSet(), FiigColors.BY_SEMANTIC_TYPE.keys)
        val tags = FiigColorSettingsPage().additionalHighlightingTagToDescriptorMap
        for (tag in listOf("activityCall", "ruleCall", "gatewayCall")) {
            assertTrue(FiigColorSettingsPage().demoText.contains("<$tag>"))
            assertNotNull(tags[tag])
        }
    }

    fun testBracesMatch() {
        myFixture.configureByText("a.fiig", "rule a(x: int) -> int <caret>{\n    return (x + 1)\n}\n")
        val offset = BraceMatchingUtil.getMatchedBraceOffset(myFixture.editor, true, myFixture.file)
        assertEquals(myFixture.file.text.lastIndexOf('}'), offset)
    }

    fun testCommentLine() {
        myFixture.configureByText("a.fiig", "<caret>rule a() -> int {\n    return 0\n}\n")
        myFixture.performEditorAction(IdeActions.ACTION_COMMENT_LINE)
        assertTrue(myFixture.editor.document.text, myFixture.editor.document.text.startsWith("//rule a()"))
    }

    fun testFolding() {
        myFixture.configureByText("a.fiig", "// one\n// two\nrule a() -> int {\n    if true {\n        return 1\n    }\n    return 0\n}\n")
        val regions = FiigFoldingBuilder().buildFoldRegions(myFixture.file, myFixture.editor.document, false)
        assertEquals("two blocks and one comment run", 3, regions.size)
    }

    fun testRunTargetsFromTheGutter() {
        fun keyword(text: String) = myFixture.configureByText("a.fiig", text).let { file ->
            file.firstChild.siblings().first { it.elementType == FiigTokens.KEYWORD }
        }
        val contributor = FiigRunLineMarkerContributor()
        val workflow = keyword("// Reads one.\nworkflow readCustomer(id: string) -> int {\n    return 0\n}\n")
        assertEquals(FiigCommand.RUN, FiigRunTargets.at(workflow)?.command)
        assertEquals("readCustomer", FiigRunTargets.at(workflow)?.decl?.name)
        assertNotNull(contributor.getInfo(workflow))
        val adapter = keyword("adapter http customerApi {\n}\n")
        assertEquals(FiigCommand.SERVE, FiigRunTargets.at(adapter)?.command)
        assertEquals("customerApi", FiigRunTargets.at(adapter)?.decl?.name)
        val rule = keyword("rule check(n: int) -> bool {\n    return n > 0\n}\n")
        assertEquals(FiigCommand.CHECK, FiigRunTargets.at(rule)?.command)
        val cli = keyword("adapter cli tools {\n}\n")
        assertEquals(FiigCommand.CHECK, FiigRunTargets.at(cli)?.command)
    }

    fun testCommandLines() {
        val type = FiigRunConfigurationType()
        val config = type.configurationFactories[0].createTemplateConfiguration(project) as FiigRunConfiguration
        val o = config.options
        o.command = FiigCommand.RUN.name
        o.packageDir = "/work/app"
        o.target = "readCustomer"
        o.argsJson = """{"id": "c1"}"""
        o.capsFile = "/work/app/caps.json"
        o.workspace = "/work"
        assertEquals(
            listOf("fiig", "run", "-caps", "/work/app/caps.json", "-args", """{"id": "c1"}""", "-workspace", "/work", "/work/app", "readCustomer"),
            config.commandLine("fiig").getCommandLineList(null),
        )
        o.command = FiigCommand.SERVE.name
        o.target = "ordersApi shop.payments.paymentsApi"
        o.addr = ":9090"
        assertEquals(
            listOf("fiig", "serve", "-caps", "/work/app/caps.json", "-addr", ":9090", "-workspace", "/work", "/work/app", "ordersApi", "shop.payments.paymentsApi"),
            config.commandLine("fiig").getCommandLineList(null),
        )
        o.command = FiigCommand.CHECK.name
        assertEquals(listOf("fiig", "check", "-workspace", "/work", "/work/app"), config.commandLine("fiig").getCommandLineList(null))
    }

    fun testNaming() {
        assertEquals("getCustomer", FiigNaming.declarationName("gateway", "get customer"))
        assertEquals("getCustomer", FiigNaming.declarationName("gateway", "GetCustomer"))
        assertEquals("NewCustomer", FiigNaming.declarationName("model", "new_customer"))
        assertEquals("GetCustomer.fiig", FiigNaming.fileName("getCustomer"))
        assertEquals("NewCustomer.fiig", FiigNaming.fileName("NewCustomer"))
        assertEquals("HttpCustomerApi.fiig", FiigNaming.fileName("httpCustomerApi"))
    }

    /** Every template, rendered, is a declaration `fiig check` accepts (when -PfiigBinary is given). */
    fun testTemplatesCheck() {
        val dir = Files.createTempDirectory("fiig-templates").toFile()
        val manager = FileTemplateManager.getInstance(project)
        for (kind in FiigNaming.KINDS) {
            val template = manager.getInternalTemplate(FiigNaming.template(kind))
            val decl = FiigNaming.declarationName(kind, "sample $kind")
            val props = manager.defaultProperties.apply { this["DECL_NAME"] = decl }
            val text = template.getText(props)
            assertTrue("$kind: $text", text.contains(decl))
            File(dir, FiigNaming.fileName(decl)).writeText(text)
        }
        val binary = System.getProperty("fiig.binary").orEmpty()
        if (binary.isEmpty()) return
        val process = ProcessBuilder(binary, "check", dir.absolutePath).redirectErrorStream(true).start()
        val output = process.inputStream.bufferedReader().readText()
        assertTrue(process.waitFor(60, TimeUnit.SECONDS))
        assertEquals("fiig check on the templates:\n$output", 0, process.exitValue())
    }

    fun testJsonSchemas() {
        assertTrue(FiigJsonSchemas.isCapabilitiesFile("caps.json"))
        assertTrue(FiigJsonSchemas.isCapabilitiesFile("caps.postgres.json"))
        assertTrue(FiigJsonSchemas.isCapabilitiesFile("capabilities.json"))
        assertFalse(FiigJsonSchemas.isCapabilitiesFile("package.json"))
        for (resource in listOf("/schemas/fiig.pkg.schema.json", "/schemas/capabilities.schema.json")) {
            assertNotNull(resource, JsonSchemaProviderFactory.getResourceFile(FiigJsonSchemas::class.java, resource))
        }
        assertEquals(2, FiigJsonSchemas().getProviders(project).size)
    }

    fun testLanguageServerCommand() {
        val descriptor = FiigLspServerDescriptor(project, "/opt/fiig/bin/fiig")
        assertEquals(listOf("/opt/fiig/bin/fiig", "lsp"), descriptor.createCommandLine().getCommandLineList(null))
        assertTrue(descriptor.isSupportedFile(myFixture.configureByText("x.fiig", "").virtualFile))
    }
}
