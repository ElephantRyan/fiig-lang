package dev.fiig.intellij

import com.intellij.execution.Executor
import com.intellij.execution.configurations.CommandLineState
import com.intellij.execution.configurations.ConfigurationFactory
import com.intellij.execution.configurations.ConfigurationTypeBase
import com.intellij.execution.configurations.GeneralCommandLine
import com.intellij.execution.configurations.RunConfiguration
import com.intellij.execution.configurations.RunConfigurationBase
import com.intellij.execution.configurations.RunConfigurationOptions
import com.intellij.execution.configurations.RuntimeConfigurationError
import com.intellij.execution.filters.Filter
import com.intellij.execution.filters.OpenFileHyperlinkInfo
import com.intellij.execution.process.KillableColoredProcessHandler
import com.intellij.execution.process.ProcessHandler
import com.intellij.execution.process.ProcessTerminatedListener
import com.intellij.execution.runners.ExecutionEnvironment
import com.intellij.openapi.components.BaseState
import com.intellij.openapi.options.SettingsEditor
import com.intellij.openapi.project.Project
import com.intellij.openapi.ui.ComboBox
import com.intellij.openapi.util.NotNullLazyValue
import com.intellij.openapi.vfs.LocalFileSystem
import com.intellij.ui.dsl.builder.AlignX
import com.intellij.ui.dsl.builder.panel
import com.intellij.ui.components.JBTextField
import com.intellij.util.execution.ParametersListUtil
import java.io.File
import javax.swing.JComponent

/** ADR 0090 §4: what a fiig run configuration runs. */
enum class FiigCommand(val title: String) {
    RUN("Run"), SERVE("Serve"), CHECK("Check");

    override fun toString(): String = title
}

class FiigRunConfigurationType : ConfigurationTypeBase(
    ID, "fiig", "Run, serve, or check a fiig package",
    NotNullLazyValue.createValue { FiigIcons.FILE },
) {
    init {
        addFactory(FiigConfigurationFactory(this))
    }

    companion object {
        const val ID = "FiigRunConfiguration"
    }
}

class FiigConfigurationFactory(type: FiigRunConfigurationType) : ConfigurationFactory(type) {
    override fun getId(): String = FiigRunConfigurationType.ID
    override fun createTemplateConfiguration(project: Project): RunConfiguration = FiigRunConfiguration(project, this, "fiig")
    override fun getOptionsClass(): Class<out BaseState> = FiigRunConfigurationOptions::class.java
}

class FiigRunConfigurationOptions : RunConfigurationOptions() {
    var command by string(FiigCommand.RUN.name)
    var packageDir by string("")
    /** Run: the declaration; Serve: the adapters, space-separated. */
    var target by string("")
    var argsJson by string("")
    var capsFile by string("")
    var envFile by string("")
    var addr by string("")
    var workspace by string("")
}

class FiigRunConfiguration(project: Project, factory: ConfigurationFactory, name: String) :
    RunConfigurationBase<FiigRunConfigurationOptions>(project, factory, name) {

    public override fun getOptions(): FiigRunConfigurationOptions = super.getOptions() as FiigRunConfigurationOptions

    val command: FiigCommand get() = runCatching { FiigCommand.valueOf(options.command ?: "") }.getOrDefault(FiigCommand.RUN)

    override fun getConfigurationEditor(): SettingsEditor<out RunConfiguration> = FiigRunConfigurationEditor()

    override fun checkConfiguration() {
        if (FiigBinary.find() == null) throw RuntimeConfigurationError("No fiig binary: set it in Settings | Languages & Frameworks | fiig, or put fiig on PATH")
        if (options.packageDir.isNullOrBlank()) throw RuntimeConfigurationError("Choose the package directory")
        if (command != FiigCommand.CHECK && options.target.isNullOrBlank()) {
            throw RuntimeConfigurationError(if (command == FiigCommand.RUN) "Name the declaration to run" else "Name the adapters to serve")
        }
    }

    /** The command line ADR 0090 §4's table gives each kind. */
    fun commandLine(binary: String): GeneralCommandLine {
        val o = options
        val args = mutableListOf(binary, command.name.lowercase())
        fun flag(name: String, value: String?) {
            if (!value.isNullOrBlank()) args += listOf(name, value)
        }
        if (command != FiigCommand.CHECK) {
            flag("-caps", o.capsFile)
            flag("-env-file", o.envFile)
        }
        if (command == FiigCommand.RUN) flag("-args", o.argsJson)
        if (command == FiigCommand.SERVE) flag("-addr", o.addr)
        flag("-workspace", o.workspace)
        args += o.packageDir ?: "."
        if (command != FiigCommand.CHECK) args += ParametersListUtil.parse(o.target ?: "")
        return GeneralCommandLine(args).withWorkDirectory(o.packageDir).withCharset(Charsets.UTF_8)
    }

    override fun getState(executor: Executor, environment: ExecutionEnvironment): CommandLineState =
        object : CommandLineState(environment) {
            init {
                addConsoleFilters(FiigLocationFilter(project, options.packageDir ?: ""))
            }

            override fun startProcess(): ProcessHandler {
                val binary = FiigBinary.find() ?: throw com.intellij.execution.ExecutionException("No fiig binary found")
                val handler = KillableColoredProcessHandler(commandLine(binary))
                ProcessTerminatedListener.attach(handler)
                return handler
            }
        }
}

/** ADR 0090 §4: `file.fiig:3:5` in output opens the file there. */
class FiigLocationFilter(private val project: Project, private val packageDir: String) : Filter {
    override fun applyFilter(line: String, entireLength: Int): Filter.Result? {
        val items = PATTERN.findAll(line).mapNotNull { m ->
            val (path, lineNo, column) = m.destructured
            val file = File(path).let { if (it.isAbsolute) it else File(packageDir, path) }
            val vfile = LocalFileSystem.getInstance().findFileByIoFile(file) ?: return@mapNotNull null
            val start = entireLength - line.length + m.range.first
            Filter.ResultItem(start, start + m.value.length, OpenFileHyperlinkInfo(project, vfile, lineNo.toInt() - 1, column.toInt() - 1))
        }.toList()
        return if (items.isEmpty()) null else Filter.Result(items)
    }

    private companion object {
        val PATTERN = Regex("""([^\s:"'(]+\.fiig):(\d+):(\d+)""")
    }
}

class FiigRunConfigurationEditor : SettingsEditor<FiigRunConfiguration>() {
    private val command = ComboBox(FiigCommand.entries.toTypedArray())
    private val packageDir = JBTextField()
    private val target = JBTextField()
    private val argsJson = JBTextField()
    private val capsFile = JBTextField()
    private val envFile = JBTextField()
    private val addr = JBTextField()
    private val workspace = JBTextField()

    override fun resetEditorFrom(c: FiigRunConfiguration) {
        val o = c.options
        command.selectedItem = c.command
        packageDir.text = o.packageDir ?: ""
        target.text = o.target ?: ""
        argsJson.text = o.argsJson ?: ""
        capsFile.text = o.capsFile ?: ""
        envFile.text = o.envFile ?: ""
        addr.text = o.addr ?: ""
        workspace.text = o.workspace ?: ""
    }

    override fun applyEditorTo(c: FiigRunConfiguration) {
        val o = c.options
        o.command = (command.selectedItem as FiigCommand).name
        o.packageDir = packageDir.text
        o.target = target.text
        o.argsJson = argsJson.text
        o.capsFile = capsFile.text
        o.envFile = envFile.text
        o.addr = addr.text
        o.workspace = workspace.text
    }

    override fun createEditor(): JComponent = panel {
        row("Command:") { cell(command) }
        row("Package directory:") { cell(packageDir).align(AlignX.FILL) }
        row("Declaration / adapters:") { cell(target).align(AlignX.FILL).comment("Run: a declaration. Serve: adapters, separated by spaces.") }
        row("-args (JSON):") { cell(argsJson).align(AlignX.FILL) }
        row("-caps:") { cell(capsFile).align(AlignX.FILL) }
        row("-env-file:") { cell(envFile).align(AlignX.FILL) }
        row("-addr:") { cell(addr).align(AlignX.FILL) }
        row("-workspace:") { cell(workspace).align(AlignX.FILL) }
    }
}
