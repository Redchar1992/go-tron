package org.tron.tools.vmoracle;

import com.google.gson.Gson;
import com.google.gson.GsonBuilder;
import java.io.BufferedReader;
import java.io.InputStreamReader;
import java.io.PrintWriter;
import java.nio.charset.StandardCharsets;
import java.util.LinkedHashMap;
import java.util.Map;
import org.tron.common.parameter.CommonParameter;

/** Persistent NDJSON entry point for the java-tron differential VM oracle. */
public final class JtronOracleMain {

  private static final String JAVA_TRON_VERSION = "GreatVoyage-v4.8.1.1";

  private JtronOracleMain() {
  }

  public static void main(String[] args) throws Exception {
    CommonParameter.getInstance().setDebug(true);

    Gson gson = new GsonBuilder().disableHtmlEscaping().create();
    BufferedReader input = new BufferedReader(
        new InputStreamReader(System.in, StandardCharsets.UTF_8));
    PrintWriter output = new PrintWriter(System.out, true);

    String line;
    while ((line = input.readLine()) != null) {
      if (line.trim().isEmpty()) {
        continue;
      }
      try {
        OracleTypes.Request request = gson.fromJson(line, OracleTypes.Request.class);
        if (request != null && "ping".equals(request.method)) {
          Map<String, String> pong = new LinkedHashMap<>();
          pong.put("status", "ok");
          pong.put("javaVersion", System.getProperty("java.version"));
          pong.put("javaTronVersion", JAVA_TRON_VERSION);
          output.println(gson.toJson(pong));
          continue;
        }
        output.println(gson.toJson(OracleExecutor.execute(request)));
      } catch (Throwable error) {
        Map<String, String> failure = new LinkedHashMap<>();
        failure.put("error", error.getClass().getSimpleName() + ": " + safeMessage(error));
        output.println(gson.toJson(failure));
      }
    }
  }

  private static String safeMessage(Throwable error) {
    return error.getMessage() == null ? "no message" : error.getMessage();
  }
}
