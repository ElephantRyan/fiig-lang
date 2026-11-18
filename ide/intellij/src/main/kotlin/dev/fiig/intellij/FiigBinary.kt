package dev.fiig.intellij

import com.intellij.execution.configurations.GeneralCommandLine
import com.intellij.execution.configurations.PathEnvironmentVariableUtil
import com.intellij.execution.util.ExecUtil
import com.intellij.openapi.components.PersistentStateComponent
import com.intellij.openapi.components.Service
import com.intellij.openapi.components.State
import com.intellij.openapi.components.Storage
import com.intellij.openapi.components.service
import com.intellij.openapi.util.SystemInfo
import java.io.File

/** ADR 0090 §3: where the `fiig` binary is, set in Settings or found on PATH. */
@Service(Service.Level.APP)
@State(name = "FiigSettings", storages = [Storage("fiig.xml")])
class FiigSettings : PersistentStateComponent<FiigSettings.State> {
    class State {
        var binaryPath: String = ""
    }

    private var state = State()
    override fun getState(): State = state
    override fun loadState(state: State) {
        this.state = state
    }

    companion object {
        fun get(): FiigSettings = service()
    }
}

object FiigBinary {
    /** The binary to run: the configured path, or `fiig` on PATH; null when neither exists. */
    fun find(configured: String = FiigSettings.get().state.binaryPath): String? {
        if (configured.isNotBlank()) {
            return File(configured).takeIf { it.isFile }?.absolutePath
        }
        val name = if (SystemInfo.isWindows) "fiig.exe" else "fiig"
        return PathEnvironmentVariableUtil.findInPath(name)?.absolutePath
    }

    /** `fiig version`'s output, or why it couldn't be read. */
    fun version(binary: String): String = try {
        val out = ExecUtil.execAndGetOutput(GeneralCommandLine(binary, "version"), 5000)
        if (out.exitCode == 0) out.stdout.trim() else out.stderr.trim().ifEmpty { "exit code ${out.exitCode}" }
    } catch (e: Exception) {
        e.message ?: e.toString()
    }
}
