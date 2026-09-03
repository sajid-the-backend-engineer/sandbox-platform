rootProject.name = "declarative-image"

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
