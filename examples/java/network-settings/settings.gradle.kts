rootProject.name = "network-settings"

dependencyResolutionManagement {
    repositories {
        mavenLocal()
        mavenCentral()
    }
}

includeBuild("../../../libs/sdk-java") {
    dependencySubstitution {
        substitute(module("io.northrays:sdk-java")).using(project(":"))
    }
}
