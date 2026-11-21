package dev.fiig.intellij

import com.intellij.execution.actions.ConfigurationContext
import com.intellij.execution.actions.LazyRunConfigurationProducer
import com.intellij.execution.configurations.ConfigurationFactory
import com.intellij.execution.configurations.ConfigurationTypeUtil
import com.intellij.execution.lineMarker.ExecutorAction
import com.intellij.execution.lineMarker.RunLineMarkerContributor
import com.intellij.icons.AllIcons
import com.intellij.openapi.util.Ref
import com.intellij.psi.PsiElement
import com.intellij.psi.util.elementType
import java.nio.file.Files
import java.nio.file.Path

/**
 * What the gutter offers: Play on the program's `application` and on each of
 * its `serve` blocks (ADR 0132), and on each `test` (ADR 0091), and nowhere
 * else. Workflows and adapters have no Play: a program is started by its
 * application, which names what it runs.
 */
object FiigRunTargets {
    /** [arg] is what the command takes: a serve block's role, a test's name, or none. */
    data class Target(val command: FiigCommand, val name: String, val arg: String)

    fun at(element: PsiElement): Target? {
        if (element.elementType != FiigTokens.IDENTIFIER) return null
        val name = FiigDeclarations.blockName(element) ?: return null
        val depth = FiigDeclarations.depthAt(element)
        val file = element.containingFile ?: return null
        return when {
            depth == 0 && element.text == "application" -> Target(FiigCommand.SERVE, name, "")
            depth == 0 && element.text == "test" -> Target(FiigCommand.TEST, name, name)
            depth == 1 && element.text == "serve" && FiigDeclarations.iconKind(file) == "application" -> Target(FiigCommand.SERVE, name, name)
            else -> null
        }
    }
}

class FiigRunLineMarkerContributor : RunLineMarkerContributor() {
    override fun getInfo(element: PsiElement): Info? {
        val target = FiigRunTargets.at(element) ?: return null
        return Info(AllIcons.RunConfigurations.TestState.Run, ExecutorAction.getActions(0)) { "${target.command.title} ${target.name}" }
    }
}

class FiigRunConfigurationProducer : LazyRunConfigurationProducer<FiigRunConfiguration>() {
    override fun getConfigurationFactory(): ConfigurationFactory =
        ConfigurationTypeUtil.findConfigurationType(FiigRunConfigurationType::class.java).configurationFactories[0]

    private fun target(context: ConfigurationContext): Pair<FiigRunTargets.Target, Path>? {
        val element = context.psiLocation ?: return null
        val target = FiigRunTargets.at(element) ?: return null
        val path = element.containingFile?.virtualFile?.toNioPath() ?: return null
        return target to path
    }

    override fun setupConfigurationFromContext(configuration: FiigRunConfiguration, context: ConfigurationContext, sourceElement: Ref<PsiElement>): Boolean {
        val (target, file) = target(context) ?: return false
        val pkg = FiigDeclarations.packageDir(file)
        val o = configuration.options
        o.command = target.command.name
        o.packageDir = pkg.toString()
        o.target = target.arg
        o.workspace = context.project.basePath ?: ""
        o.capsFile = pkg.resolve("caps.json").takeIf { Files.isRegularFile(it) }?.toString() ?: ""
        configuration.name = "${target.command.title} ${target.name}"
        return true
    }

    override fun isConfigurationFromContext(configuration: FiigRunConfiguration, context: ConfigurationContext): Boolean {
        val (target, file) = target(context) ?: return false
        val o = configuration.options
        return configuration.command == target.command &&
            o.packageDir == FiigDeclarations.packageDir(file).toString() &&
            (o.target ?: "") == target.arg
    }
}
