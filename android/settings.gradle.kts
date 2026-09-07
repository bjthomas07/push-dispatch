pluginManagement { repositories { google(); mavenCentral(); gradlePluginPortal() } }
dependencyResolutionManagement {
    repositoriesMode.set(RepositoriesMode.FAIL_ON_PROJECT_REPOS)
    repositories { google(); mavenCentral() }
}
rootProject.name = "push-dispatch"
include(":notifications-core", ":notifications-firebase")
project(":notifications-core").projectDir = file("core")
project(":notifications-firebase").projectDir = file("firebase")
