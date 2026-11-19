import org.jetbrains.intellij.platform.gradle.IntelliJPlatformType
import org.jetbrains.intellij.platform.gradle.TestFrameworkType
import org.jetbrains.kotlin.gradle.dsl.JvmTarget

plugins {
    id("org.jetbrains.kotlin.jvm") version "2.4.21"
    id("org.jetbrains.intellij.platform") version "2.20.1"
}

group = "dev.fiig"
version = providers.gradleProperty("pluginVersion").get()

repositories {
    mavenCentral()
    intellijPlatform {
        defaultRepositories()
    }
}

val localIde = providers.gradleProperty("localIde").orNull?.takeIf { file(it).isDirectory }

dependencies {
    intellijPlatform {
        if (localIde != null) {
            local(localIde)
        } else {
            create(IntelliJPlatformType.IntellijIdeaUltimate, providers.gradleProperty("platformVersion").get())
        }
        bundledPlugin("com.intellij.modules.json")
        testFramework(TestFrameworkType.Platform)
    }
    testImplementation("junit:junit:4.13.2")
}

kotlin {
    // IntelliJ 2026.2's and 2026.3's classes are Java 25 (class file 69).
    jvmToolchain(25)
    compilerOptions {
        jvmTarget = JvmTarget.JVM_21
    }
}

intellijPlatform {
    buildSearchableOptions = false
    pluginConfiguration {
        version = providers.gradleProperty("pluginVersion")
        ideaVersion {
            sinceBuild = "262"
            untilBuild = "263.*"
        }
    }
    pluginVerification {
        // ADR 0090 §1a: every release in the range.
        ides {
            if (localIde != null) {
                local(localIde)
            } else {
                for (v in providers.gradleProperty("verifyVersions").get().split(",")) {
                    create(IntelliJPlatformType.IntellijIdeaUltimate, v.trim())
                }
            }
        }
    }
}

// ADR 0090 §6: the JSON schemas live with the docs and ship in the plugin.
val copySchemas = tasks.register<Copy>("copySchemas") {
    from(rootDir.resolve("../../docs/schemas"))
    into(layout.buildDirectory.dir("generated/schemas/schemas"))
}
sourceSets.main {
    resources.srcDir(layout.buildDirectory.dir("generated/schemas"))
}
tasks.processResources { dependsOn(copySchemas) }

tasks.test {
    // The lexer test reads the repository's examples (ADR 0090 §7).
    systemProperty("fiig.examples", rootDir.resolve("../../examples").absolutePath)
    systemProperty("fiig.binary", providers.gradleProperty("fiigBinary").orElse("").get())
}
