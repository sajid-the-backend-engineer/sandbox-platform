plugins {
    application
}

group = "io.northrays.examples"
version = "0.1.0"

java {
    sourceCompatibility = JavaVersion.VERSION_11
    targetCompatibility = JavaVersion.VERSION_11
}

repositories {
    mavenLocal()
    mavenCentral()
}

dependencies {
    implementation("io.northrays:sdk-java")
}

application {
    mainClass.set("io.northrays.examples.DeclarativeImage")
}
