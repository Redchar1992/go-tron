package org.tron.tools.vmoracle;

import com.google.gson.annotations.SerializedName;
import java.util.List;
import java.util.Map;

/** DTOs matching go-tron's internal/vmoracle JSON schema. */
final class OracleTypes {

  private OracleTypes() {
  }

  static final class Request {
    String method;
    World world;
    Tx tx;
  }

  static final class World {
    int version;
    DynamicProps dynamicProps = new DynamicProps();
    Block block = new Block();
    Map<String, Account> accounts;
  }

  static final class DynamicProps {
    long totalEnergyWeight;
    long totalEnergyCurrentLimit;
    boolean supportUnfreezeDelay;
    boolean allowNewReward;
    long energyFee;
  }

  static final class Block {
    long number;
    long timestamp;
    String witness;
  }

  static final class Account {
    long balance;
    String code;
    Map<String, String> storage;
    long energyStake;
  }

  static final class Tx {
    String type;
    String owner;
    String contract;
    String data;
    String bytecode;
    long callValue;
    long feeLimit;
    String txID;
  }

  static final class Execution {
    String result;
    String vmError;
    @SerializedName("return")
    String returnData;
    long energyUsed;
    long energyFee;
    long originEnergyUsage;
    Map<String, Map<String, String>> storageWrites;
    List<LogEntry> logs;
    String createdAddress;
  }

  static final class LogEntry {
    String address;
    List<String> topics;
    String data;
  }
}
